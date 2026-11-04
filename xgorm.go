package xgorm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

var (
	// mu guards registry membership; network I/O and pool shutdown run outside the lock.
	mu        sync.RWMutex
	instances = make(map[string]*gorm.DB)
)

// preparedInstance pairs an initialized database with its registry alias.
type preparedInstance struct {
	alias string
	db    *gorm.DB
}

// driverConnection keeps pool ownership separate from GORM's configurable wrappers.
type driverConnection struct {
	dialect              gorm.Dialector
	pool                 *sql.DB
	finishInitialization func()
}

// Init initializes and health-checks a batch of databases before registering it.
// Failure leaves the registry unchanged and closes pools created by this call.
// ctx controls context-aware initialization work and is not retained by registered databases.
// Initialize during startup and do not overlap initialization with registry shutdown.
func Init(ctx context.Context, configs []Config) error {
	if ctx == nil {
		return errors.New("xgorm: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := prepareConnections(ctx, configs)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return errors.Join(err, closePrepared(prepared))
	}
	if err = registerPrepared(prepared); err != nil {
		return errors.Join(err, closePrepared(prepared))
	}
	return nil
}

// prepareConnections validates the batch before opening pools and cleans up partial results on failure.
func prepareConnections(ctx context.Context, configs []Config) ([]preparedInstance, error) {
	if len(configs) == 0 {
		return nil, errors.New("xgorm: no connections configured")
	}

	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		if err := config.Validate(); err != nil {
			return nil, fmt.Errorf("xgorm %q: %w", config.Alias, err)
		}
		if _, exists := seen[config.Alias]; exists {
			return nil, fmt.Errorf("xgorm: duplicate connection alias %q", config.Alias)
		}
		seen[config.Alias] = struct{}{}
	}

	prepared := make([]preparedInstance, 0, len(configs))
	for _, config := range configs {
		connection, err := open(ctx, config)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("xgorm %q: %w", config.Alias, err), closePrepared(prepared))
		}
		prepared = append(prepared, preparedInstance{alias: config.Alias, db: connection})
	}
	return prepared, nil
}

// registerPrepared checks existing aliases and publishes the batch under one registry lock.
func registerPrepared(prepared []preparedInstance) error {
	mu.Lock()
	defer mu.Unlock()

	for _, item := range prepared {
		if _, exists := instances[item.alias]; exists {
			return fmt.Errorf("xgorm: alias %q is already registered", item.alias)
		}
	}
	for _, item := range prepared {
		instances[item.alias] = item.db
	}
	return nil
}

// open creates an owned pool and verifies connectivity without retaining the startup context.
func open(ctx context.Context, config Config) (_ *gorm.DB, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	startupCtx, cancel := context.WithTimeout(ctx, time.Duration(config.PingTimeout)*time.Second)
	defer cancel()

	driver, err := newDriverConnection(startupCtx, config)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	ready := false
	defer func() {
		// Drop temporary driver state before canceling the startup context.
		if driver.finishInitialization != nil {
			driver.finishInitialization()
		}
		// Retain ownership even when a failing plugin changes GORM's pool reference.
		if !ready {
			err = errors.Join(err, driver.pool.Close())
		}
	}()
	configurePool(driver.pool, config)

	options := gorm.Config{}
	if config.GORM != nil {
		options = *config.GORM
	}
	// Drivers and plugins can mutate these maps; aliases must not share their registrations.
	options.ClauseBuilders = maps.Clone(options.ClauseBuilders)
	options.Plugins = maps.Clone(options.Plugins)
	if naming, ok := options.NamingStrategy.(*schema.NamingStrategy); ok && naming != nil {
		// PostgreSQL fills identifier limits in the built-in naming strategy during initialization.
		options.NamingStrategy = *naming
	}
	options.DisableAutomaticPing = true
	if options.Logger == nil {
		options.Logger = logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		})
	}

	// Every dialect receives an already configured pool before its probes or user plugins run.
	connection, err := gorm.Open(driver.dialect, &options)
	if err != nil {
		return nil, fmt.Errorf("initialize connection: %w", err)
	}
	if config.Debug {
		connection = connection.Debug()
	}

	pool, err := connection.DB()
	if err != nil {
		return nil, fmt.Errorf("get connection pool: %w", err)
	}

	if pool != driver.pool {
		return nil, errors.New("initialize connection: plugins must retain the owned connection pool")
	}
	if err = pool.PingContext(startupCtx); err != nil {
		return nil, fmt.Errorf("ping connection: %w", err)
	}
	ready = true
	return connection, nil
}

// configurePool applies explicit connection limits and recycling policies.
func configurePool(pool *sql.DB, config Config) {
	pool.SetMaxOpenConns(config.MaxOpenConns)
	pool.SetMaxIdleConns(config.MaxIdleConns)
	pool.SetConnMaxLifetime(time.Duration(config.ConnMaxLifetime) * time.Second)
	pool.SetConnMaxIdleTime(time.Duration(config.ConnMaxIdleTime) * time.Second)
}

// newDriverConnection selects a native dialect and creates its owned SQL pool.
func newDriverConnection(ctx context.Context, config Config) (*driverConnection, error) {
	switch config.Driver {
	case DriverMySQL:
		return newMySQLConnection(ctx, config.DSN)
	case DriverPostgres:
		return newPostgresConnection(config.DSN)
	case DriverSQLite:
		return newSQLiteConnection(config.DSN)
	case DriverSQLServer:
		return newSQLServerConnection(config.DSN)
	default:
		return nil, fmt.Errorf("unsupported driver %q", config.Driver)
	}
}

// Get returns the shared database for alias, or nil if no database is registered.
// Use WithContext or Session for request-specific settings; do not mutate or close the shared root.
func Get(alias string) *gorm.DB {
	mu.RLock()
	defer mu.RUnlock()
	return instances[alias]
}

// Close removes and closes a named database, returning nil if the alias is absent.
// Stop its consumers first; pool closure cannot stop arbitrary application goroutines.
func Close(alias string) error {
	mu.Lock()
	db, exists := instances[alias]
	if exists {
		delete(instances, alias)
	}
	mu.Unlock()

	if !exists {
		return nil
	}
	return closeConnection(alias, db)
}

// CloseAll removes every registered database and attempts to close each pool.
// Call it after all database consumers have stopped. Errors retain their database aliases.
func CloseAll() error {
	mu.Lock()
	prepared := make([]preparedInstance, 0, len(instances))
	for alias, db := range instances {
		prepared = append(prepared, preparedInstance{alias: alias, db: db})
		delete(instances, alias)
	}
	mu.Unlock()
	return closePrepared(prepared)
}

// closePrepared attempts every pool close and aggregates errors with their aliases.
func closePrepared(prepared []preparedInstance) error {
	var closeErrors []error
	for _, item := range prepared {
		closeErrors = append(closeErrors, closeConnection(item.alias, item.db))
	}
	return errors.Join(closeErrors...)
}

// closeConnection closes the SQL pool underlying a registered GORM database.
func closeConnection(alias string, db *gorm.DB) error {
	pool, err := db.DB()
	if err != nil {
		return fmt.Errorf("xgorm %q: get connection pool: %w", alias, err)
	}
	if err := pool.Close(); err != nil {
		return fmt.Errorf("xgorm %q: close connection pool: %w", alias, err)
	}
	return nil
}
