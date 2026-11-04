package xgorm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type exampleRecord struct {
	ID   uint `gorm:"primaryKey"`
	Name string
}
type capturePlugin struct {
	pool  *sql.DB
	err   error
	calls atomic.Int32
}

func (p *capturePlugin) Name() string { return "capture" }
func (p *capturePlugin) Initialize(db *gorm.DB) error {
	p.calls.Add(1)
	var err error
	p.pool, err = db.DB()
	if err != nil {
		return err
	}
	return p.err
}
func TestInitValidationAndAtomicRegistry(t *testing.T) {
	defer CloseAll()
	ctx := context.Background()
	if err := Init(nil, []Config{sqliteConfig("nil")}); err == nil {
		t.Fatal("nil context accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := Init(canceled, []Config{sqliteConfig("canceled")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Init = %v", err)
	}
	if err := Init(ctx, nil); err == nil {
		t.Fatal("empty batch accepted")
	}
	if err := Init(ctx, []Config{sqliteConfig("duplicate"), sqliteConfig("duplicate")}); err == nil {
		t.Fatal("duplicate batch accepted")
	}
	if Get("duplicate") != nil {
		t.Fatal("invalid batch published")
	}
	if err := Init(ctx, []Config{sqliteConfig("existing")}); err != nil {
		t.Fatal(err)
	}
	old := Get("existing")
	plugin := &capturePlugin{}
	replacement := sqliteConfig("existing")
	replacement.GORM = &gorm.Config{Plugins: map[string]gorm.Plugin{"capture": plugin}}
	if err := Init(ctx, []Config{sqliteConfig("new"), replacement}); err == nil {
		t.Fatal("registered alias replaced")
	}
	if Get("existing") != old || Get("new") != nil {
		t.Fatal("failed batch changed registry")
	}
	if plugin.pool == nil || plugin.pool.Ping() == nil {
		t.Fatal("rejected batch pool not closed")
	}
}
func TestInitFailureClosesEveryCreatedPool(t *testing.T) {
	defer CloseAll()
	first := &capturePlugin{}
	failure := errors.New("plugin failed")
	second := &capturePlugin{err: failure}
	a, b := sqliteConfig("first"), sqliteConfig("second")
	a.GORM = &gorm.Config{Plugins: map[string]gorm.Plugin{"capture": first}}
	b.GORM = &gorm.Config{Plugins: map[string]gorm.Plugin{"capture": second}}
	if err := Init(t.Context(), []Config{a, b}); !errors.Is(err, failure) {
		t.Fatalf("Init = %v", err)
	}
	for _, p := range []*capturePlugin{first, second} {
		if p.pool == nil || p.pool.Ping() == nil {
			t.Fatal("partial pool remained open")
		}
	}
	if Get("first") != nil || Get("second") != nil {
		t.Fatal("partial batch registered")
	}
}
func TestInitSQLiteCRUDTransactionsAndStartupContext(t *testing.T) {
	defer CloseAll()
	ctx, cancel := context.WithCancel(t.Context())
	config := sqliteConfig("crud")
	config.GORM = &gorm.Config{PrepareStmt: true}
	if err := Init(ctx, []Config{config}); err != nil {
		t.Fatal(err)
	}
	cancel()
	db := Get("crud")
	if db.Statement.Context.Err() != nil {
		t.Fatal("startup context retained")
	}
	if err := db.AutoMigrate(&exampleRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return tx.Create(&exampleRecord{Name: "kept"}).Error }); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback")
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&exampleRecord{Name: "discarded"}).Error; err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&exampleRecord{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var record exampleRecord
	if err := db.WithContext(t.Context()).First(&record).Error; err != nil || record.Name != "kept" {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if pool.Stats().MaxOpenConnections != 1 {
		t.Fatal("pool limit not configured")
	}
	if err = Close("crud"); err != nil {
		t.Fatal(err)
	}
	if Get("crud") != nil || pool.Ping() == nil {
		t.Fatal("Close did not remove and close pool")
	}
	if err = Close("crud"); err != nil {
		t.Fatal(err)
	}
}
func TestInitSQLiteFileAndFreshGORMSettings(t *testing.T) {
	defer CloseAll()
	builders := map[string]clause.ClauseBuilder{}
	plugins := map[string]gorm.Plugin{}
	settings := &gorm.Config{ClauseBuilders: builders, Plugins: plugins}
	config := sqliteConfig("file")
	config.DSN = filepath.Join(t.TempDir(), "records.db")
	config.GORM = settings
	if err := Init(t.Context(), []Config{config}); err != nil {
		t.Fatal(err)
	}
	if len(builders) != 0 || len(plugins) != 0 || settings.DisableAutomaticPing {
		t.Fatal("caller GORM config mutated")
	}
	db := Get("file")
	if err := db.AutoMigrate(&exampleRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&exampleRecord{Name: "persisted"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Close("file"); err != nil {
		t.Fatal(err)
	}
	if err := Init(t.Context(), []Config{config}); err != nil {
		t.Fatal(err)
	}
	var result exampleRecord
	if err := Get("file").First(&result).Error; err != nil || result.Name != "persisted" {
		t.Fatalf("reopened=%+v err=%v", result, err)
	}
}
func TestCloseErrorsKeepAliasAndRemoveRegistry(t *testing.T) {
	defer CloseAll()
	sentinel := errors.New("close failed")
	pool := sql.OpenDB(&fakeConnector{closeErr: sentinel})
	if err := pool.Ping(); err != nil {
		t.Fatal(err)
	}
	instances["broken"] = &gorm.DB{Config: &gorm.Config{ConnPool: pool}}
	if err := Close("broken"); !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("Close = %v", err)
	}
	if Get("broken") != nil {
		t.Fatal("failed Close kept registry alias")
	}
	for _, alias := range []string{"one", "two"} {
		p := sql.OpenDB(&fakeConnector{closeErr: sentinel})
		if err := p.Ping(); err != nil {
			t.Fatal(err)
		}
		instances[alias] = &gorm.DB{Config: &gorm.Config{ConnPool: p}}
	}
	err := CloseAll()
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "one") || !strings.Contains(err.Error(), "two") {
		t.Fatalf("CloseAll = %v", err)
	}
	if Get("one") != nil || Get("two") != nil {
		t.Fatal("CloseAll retained registry")
	}
}
func ExampleInit() {
	config := Config{Alias: "example", Driver: DriverSQLite, DSN: ":memory:", PingTimeout: 1, MaxOpenConns: 1, MaxIdleConns: 1}
	if err := Init(context.Background(), []Config{config}); err != nil {
		panic(err)
	}
	defer Close("example")
	db := Get("example")
	if err := db.AutoMigrate(&exampleRecord{}); err != nil {
		panic(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return tx.Create(&exampleRecord{Name: "Ada"}).Error }); err != nil {
		panic(err)
	}
	var record exampleRecord
	if err := db.First(&record).Error; err != nil {
		panic(err)
	}
	fmt.Println(record.Name)
	// Output: Ada
}
