package xgorm

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	defaultMaxIdleConn     = 10
	defaultMaxOpenConn     = 100
	defaultConnMaxLifetime = 1 * time.Hour
	defaultPingTimeout     = 10 // seconds
)

// Driver defines the type for supported database engines.
type Driver string

const (
	DriverMySQL     = "mysql"
	DriverPostgres  = "postgres"
	DriverSQLite    = "sqlite"
	DriverSQLServer = "sqlserver"
)

// Validate ensures the driver is supported by gorm.
func (d Driver) Validate() error {
	switch d {
	case DriverMySQL, DriverPostgres, DriverSQLite, DriverSQLServer:
		return nil
	default:
		return fmt.Errorf("xgorm: unsupported driver [%s]", d)
	}
}

// Config represents the database connection and pool settings.
type Config struct {
	// Alias uniquely identifies the database instance (e.g., "master").
	Alias string `json:"alias"`

	// Driver specifies the database engine.
	// Supported: "mysql", "postgres", "sqlite", "sqlserver". Defaults to "mysql".
	Driver Driver `json:"driver"`

	// DSN is the connection string (e.g., "user:pass@tcp(127.0.0.1:3306)/db?parseTime=True").
	DSN string `json:"dsn"`

	// MaxIdleConn sets the maximum number of connections in the idle connection pool.
	MaxIdleConn *uint `json:"max_idle_conn"`

	// MaxOpenConn sets the maximum number of open connections to the database.
	MaxOpenConn *uint `json:"max_open_conn"`

	// ConnMaxLifetime sets the maximum amount of time (in seconds) a connection may be reused.
	ConnMaxLifetime *uint `json:"conn_max_lifetime"`

	// ConnMaxIdleTime sets the maximum amount of time (in seconds) a connection may be idle.
	ConnMaxIdleTime *uint `json:"conn_max_idle_time"`

	// Debug enables GORM's verbose logging mode.
	Debug bool `json:"debug"`

	// PingTimeout specifies the maximum duration (in seconds) to wait when pinging the database
	// during initialization. Defaults to 10 seconds.
	PingTimeout *uint `json:"ping_timeout"`

	// GormConfig allows passing raw GORM configuration for advanced customization.
	GormConfig *gorm.Config `json:"-"`
}

// FillDefaults populates empty fields with production-ready default values.
func (cfg *Config) FillDefaults() {
	if cfg.Driver == "" {
		cfg.Driver = DriverMySQL
	}
	if cfg.MaxIdleConn == nil {
		val := uint(defaultMaxIdleConn)
		cfg.MaxIdleConn = &val
	}
	if cfg.MaxOpenConn == nil {
		val := uint(defaultMaxOpenConn)
		cfg.MaxOpenConn = &val
	}
	if cfg.ConnMaxLifetime == nil {
		val := uint(defaultConnMaxLifetime.Seconds())
		cfg.ConnMaxLifetime = &val
	}
	if cfg.PingTimeout == nil || *cfg.PingTimeout == 0 {
		val := uint(defaultPingTimeout)
		cfg.PingTimeout = &val
	}
}

// Validate checks for mandatory fields and driver validity.
func (cfg *Config) Validate() error {
	if cfg.Alias == "" {
		return fmt.Errorf("xgorm: alias is required")
	}
	if cfg.DSN == "" {
		return fmt.Errorf("xgorm: DSN is required for alias [%s]", cfg.Alias)
	}
	return cfg.Driver.Validate()
}
