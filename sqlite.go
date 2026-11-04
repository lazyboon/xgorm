package xgorm

import (
	"database/sql"

	"gorm.io/driver/sqlite"
)

// newSQLiteConnection creates the native SQLite dialect without opening a physical connection.
func newSQLiteConnection(dsn string) (*driverConnection, error) {
	pool, err := sql.Open(sqlite.DriverName, dsn)
	if err != nil {
		return nil, err
	}
	return &driverConnection{
		dialect: sqlite.New(sqlite.Config{DSN: dsn, Conn: pool}),
		pool:    pool,
	}, nil
}
