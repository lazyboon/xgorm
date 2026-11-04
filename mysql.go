package xgorm

import (
	"context"
	"database/sql"
	"sync/atomic"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// mysqlPool bounds the driver's background-context version probe during startup.
// Embedding sql.DB preserves transaction and prepared-statement support.
type mysqlPool struct {
	*sql.DB
	initialization atomic.Pointer[context.Context]
}

var _ gorm.GetDBConnector = (*mysqlPool)(nil)

// newMySQLConnection creates a native dialect with a context-aware startup pool.
func newMySQLConnection(ctx context.Context, dsn string) (*driverConnection, error) {
	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	connection := &mysqlPool{DB: pool}
	connection.initialization.Store(&ctx)
	return &driverConnection{
		dialect:              mysql.New(mysql.Config{DSN: dsn, Conn: connection}),
		pool:                 pool,
		finishInitialization: connection.finishInitialization,
	}, nil
}

// QueryRowContext applies the startup deadline only to otherwise unbounded background queries.
func (p *mysqlPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if ctx == context.Background() {
		if initialization := p.initialization.Load(); initialization != nil {
			ctx = *initialization
		}
	}
	// A Row is consumed later by Scan; the startup context must remain valid until initialization ends.
	return p.DB.QueryRowContext(ctx, query, args...)
}

// GetDBConn exposes the owned SQL pool to GORM, including its prepared-statement wrapper.
func (p *mysqlPool) GetDBConn() (*sql.DB, error) {
	return p.DB, nil
}

// finishInitialization releases the startup context before the database is published for normal use.
func (p *mysqlPool) finishInitialization() {
	p.initialization.Store(nil)
}
