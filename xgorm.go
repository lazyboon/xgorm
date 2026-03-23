package xgorm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

var (
	mu        sync.RWMutex
	instances = make(map[string]*gorm.DB)
)

// Init initializes multiple database instances from a slice of configurations.
func Init(configs []*Config) error {
	for _, cfg := range configs {
		if _, err := Open(cfg); err != nil {
			return err
		}
	}
	return nil
}

// Open initializes a single database connection and registers it to the manager.
func Open(cfg *Config) (*gorm.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.FillDefaults()

	mu.Lock()
	defer mu.Unlock()

	// Ensure no duplicate registration for the same alias
	if db, ok := instances[cfg.Alias]; ok {
		return db, nil
	}

	// Multi-driver dispatcher
	var dial gorm.Dialector
	switch cfg.Driver {
	case DriverPostgres:
		dial = postgres.Open(cfg.DSN)
	case DriverSQLite:
		dial = sqlite.Open(cfg.DSN)
	case DriverSQLServer:
		dial = sqlserver.Open(cfg.DSN)
	default: // Guaranteed to be MySQL by Validate/FillDefaults
		dial = mysql.Open(cfg.DSN)
	}

	gormCfg := cfg.GormConfig
	if gormCfg == nil {
		gormCfg = &gorm.Config{}
	}

	db, err := gorm.Open(dial, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("xgorm [%s]: failed to connect: %w", cfg.Alias, err)
	}

	if cfg.Debug {
		db = db.Debug()
	}

	// Setup connection pool and check health
	if err = applyPoolSettings(db, cfg); err != nil {
		return nil, err
	}

	instances[cfg.Alias] = db
	return db, nil
}

// Get retrieves a gorm.DB instance by its alias. Returns nil if not found.
func Get(alias string) *gorm.DB {
	mu.RLock()
	defer mu.RUnlock()
	return instances[alias]
}

// Close gracefully closes a specific database connection.
func Close(alias string) error {
	mu.Lock()
	defer mu.Unlock()

	db, ok := instances[alias]
	if !ok {
		return nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	delete(instances, alias)
	return sqlDB.Close()
}

// CloseAll closes all registered database connections. Useful for graceful shutdown.
func CloseAll() {
	mu.Lock()
	defer mu.Unlock()

	for alias, db := range instances {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		delete(instances, alias)
	}
}

// applyPoolSettings configures the database/sql connection pool and performs a timed Ping.
func applyPoolSettings(db *gorm.DB, cfg *Config) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	sqlDB.SetMaxIdleConns(int(*cfg.MaxIdleConn))
	sqlDB.SetMaxOpenConns(int(*cfg.MaxOpenConn))
	sqlDB.SetConnMaxLifetime(time.Duration(*cfg.ConnMaxLifetime) * time.Second)

	if cfg.ConnMaxIdleTime != nil {
		sqlDB.SetConnMaxIdleTime(time.Duration(*cfg.ConnMaxIdleTime) * time.Second)
	}

	// Health check with configurable timeout to avoid blocking during startup
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*cfg.PingTimeout)*time.Second)
	defer cancel()

	if err = sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("database [%s] unreachable: %w", cfg.Alias, err)
	}

	return nil
}
