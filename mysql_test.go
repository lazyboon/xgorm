package xgorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"io"
	"testing"
	"time"
)

type fakeConnector struct{ closeErr error }
type fakeDriver struct{}
type fakeConn struct{ closeErr error }
type fakeStmt struct{}
type fakeTx struct{}
type fakeRows struct{ done bool }

func (c *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{closeErr: c.closeErr}, nil
}
func (c *fakeConnector) Driver() driver.Driver          { return fakeDriver{} }
func (fakeDriver) Open(string) (driver.Conn, error)     { return &fakeConn{}, nil }
func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return c.closeErr }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }
func (c *fakeConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if q == "block" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &fakeRows{}, nil
}
func (c *fakeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (fakeStmt) Close() error                               { return nil }
func (fakeStmt) NumInput() int                              { return -1 }
func (fakeStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(1), nil }
func (fakeStmt) Query([]driver.Value) (driver.Rows, error)  { return &fakeRows{}, nil }
func (fakeTx) Commit() error                                { return nil }
func (fakeTx) Rollback() error                              { return nil }
func (r *fakeRows) Columns() []string                       { return []string{"value"} }
func (r *fakeRows) Close() error                            { return nil }
func (r *fakeRows) Next(v []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	v[0] = int64(1)
	return nil
}
func newFakeMySQLPool(t *testing.T) *mysqlPool {
	t.Helper()
	db := sql.OpenDB(&fakeConnector{})
	t.Cleanup(func() { _ = db.Close() })
	return &mysqlPool{DB: db}
}
func TestMySQLPoolStartupDeadlineAndRuntimeIndependence(t *testing.T) {
	t.Parallel()
	p := newFakeMySQLPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	p.initialization.Store(&ctx)
	var n int
	err := p.QueryRowContext(context.Background(), "block").Scan(&n)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup: %v", err)
	}
	p.finishInitialization()
	if err = p.QueryRowContext(context.Background(), "select 1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("runtime: n=%d err=%v", n, err)
	}
	explicit, stop := context.WithCancel(t.Context())
	stop()
	if err = p.QueryRowContext(explicit, "select 1").Scan(&n); !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit context: %v", err)
	}
}
func TestMySQLPoolPreparedStatementsAndTransactions(t *testing.T) {
	t.Parallel()
	p := newFakeMySQLPool(t)
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: p, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, PrepareStmt: true})
	if err != nil {
		t.Fatal(err)
	}
	native, err := db.DB()
	if err != nil || native != p.DB {
		t.Fatalf("pool: %v", err)
	}
	if err = db.Transaction(func(tx *gorm.DB) error { return tx.Exec("UPDATE examples SET value = ?", 1).Error }); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	var n int
	if err = db.Raw("SELECT 1").Row().Scan(&n); err != nil || n != 1 {
		t.Fatalf("prepared row: %v %d", err, n)
	}
}
