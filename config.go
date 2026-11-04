// Package xgorm manages named GORM databases and their connection pools.
// Initialize databases at startup and stop their consumers before calling [CloseAll].
package xgorm

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Driver identifies a supported GORM database dialect.
type Driver string

// Supported SQL drivers.
const (
	DriverMySQL     Driver = "mysql"
	DriverPostgres  Driver = "postgres"
	DriverSQLite    Driver = "sqlite"
	DriverSQLServer Driver = "sqlserver"
)

// Validate reports whether the driver is supported.
func (d Driver) Validate() error {
	switch d {
	case DriverMySQL, DriverPostgres, DriverSQLite, DriverSQLServer:
		return nil
	default:
		return fmt.Errorf("xgorm: unsupported driver %q", d)
	}
}

// Config defines a named database and its connection pool. Durations are expressed in seconds.
// Connection limits and PingTimeout are explicit; lifetime and idle-time limits of zero are disabled.
// Configuration and referenced objects must not be mutated during initialization.
type Config struct {
	// Alias identifies the database in the package registry and must be unique.
	Alias string `json:"alias"`

	// Driver selects the SQL dialect and driver.
	Driver Driver `json:"driver"`

	// DSN is the driver-specific data source name and may contain credentials.
	DSN string `json:"dsn"`

	// Debug enables GORM's info-level SQL logging.
	Debug bool `json:"debug"`

	// GORM supplies fresh ORM settings such as logging, naming, migrations, and plugins.
	// ConnPool and Dialector must be unset; xgorm owns connection creation and shutdown.
	// ClauseBuilders and Plugins maps are copied, but their values and other referenced objects are shared.
	// Shared plugins and callbacks must be safe for their intended concurrent use.
	// Pool wrappers must expose the original SQL pool through GORM's GetDBConnector interface.
	// Automatic GORM Ping is always disabled in favor of the bounded startup check.
	GORM *gorm.Config `json:"-"`

	// PingTimeout bounds context-aware startup checks, including MySQL version detection.
	// Driver operations or plugins that ignore context may not honor this timeout.
	PingTimeout int `json:"ping_timeout"`

	// MaxOpenConns limits the number of open connections and must be positive.
	MaxOpenConns int `json:"max_open_conns"`

	// MaxIdleConns limits retained idle connections; zero retains none.
	MaxIdleConns int `json:"max_idle_conns"`

	// ConnMaxLifetime limits connection reuse by age; zero disables this limit.
	ConnMaxLifetime int `json:"conn_max_lifetime"`

	// ConnMaxIdleTime limits how long a connection may remain idle; zero disables this limit.
	ConnMaxIdleTime int `json:"conn_max_idle_time"`
}

// Validate checks driver, connection, and pool settings without opening a connection.
func (c Config) Validate() error {
	const maxDurationSeconds = (1<<63 - 1) / int64(time.Second)
	if err := c.Driver.Validate(); err != nil {
		return err
	}

	switch {
	case strings.TrimSpace(c.Alias) == "" || c.Alias != strings.TrimSpace(c.Alias):
		return errors.New("xgorm: alias must be non-empty and contain no surrounding spaces")
	case strings.TrimSpace(c.DSN) == "":
		return errors.New("xgorm: dsn must not be empty")
	case strings.ContainsRune(c.DSN, 0):
		return errors.New("xgorm: dsn must not contain null bytes")
	case c.PingTimeout <= 0 || int64(c.PingTimeout) > maxDurationSeconds:
		return errors.New("xgorm: ping_timeout must be positive and fit within time.Duration")
	case c.MaxOpenConns <= 0:
		return errors.New("xgorm: max_open_conns must be greater than 0")
	case c.MaxIdleConns < 0 || c.MaxIdleConns > c.MaxOpenConns:
		return errors.New("xgorm: max_idle_conns must be between 0 and max_open_conns")
	case c.ConnMaxLifetime < 0 || int64(c.ConnMaxLifetime) > maxDurationSeconds:
		return errors.New("xgorm: conn_max_lifetime must be non-negative and fit within time.Duration")
	case c.ConnMaxIdleTime < 0 || int64(c.ConnMaxIdleTime) > maxDurationSeconds:
		return errors.New("xgorm: conn_max_idle_time must be non-negative and fit within time.Duration")
	}

	if c.GORM != nil {
		if c.GORM.ConnPool != nil || c.GORM.Dialector != nil {
			return errors.New("xgorm: GORM settings must not supply a connection pool or dialect")
		}
	}
	if c.Driver == DriverSQLite {
		return c.validateSQLite()
	}
	return nil
}

// validateSQLite preserves in-memory databases across idle periods and pool reuse.
func (c Config) validateSQLite() error {
	dsn := c.DSN
	if strings.HasPrefix(dsn, "file:") {
		// SQLite stops URI parsing at a fragment; a '#' in an ordinary filename is literal.
		dsn, _, _ = strings.Cut(dsn, "#")
	}
	name, rawQuery, _ := strings.Cut(dsn, "?")
	parameters, err := url.ParseQuery(rawQuery)
	if err != nil {
		return fmt.Errorf("xgorm: invalid SQLite query parameters: %w", err)
	}
	for key, values := range parameters {
		if strings.ContainsRune(key, 0) {
			return errors.New("xgorm: SQLite parameters must not contain encoded null bytes")
		}
		for _, value := range values {
			if strings.ContainsRune(value, 0) {
				return errors.New("xgorm: SQLite parameters must not contain encoded null bytes")
			}
		}
	}
	memory := name == ":memory:"
	shared := false
	if strings.HasPrefix(name, "file:") {
		// SQLite interprets memory/cache URI settings only for file: data sources.
		// Non-URI query parameters are stripped by the driver before opening the filename.
		if len(parameters["mode"]) > 1 || len(parameters["cache"]) > 1 {
			return errors.New("xgorm: SQLite mode and cache parameters must not be repeated")
		}
		filename, decodeErr := url.PathUnescape(strings.TrimPrefix(name, "file:"))
		if decodeErr != nil {
			return fmt.Errorf("xgorm: invalid SQLite URI filename: %w", decodeErr)
		}
		if strings.ContainsRune(filename, 0) {
			return errors.New("xgorm: SQLite URI filenames must not contain encoded null bytes")
		}
		memory = filename == ":memory:" || parameters.Get("mode") == "memory"
		shared = parameters.Get("cache") == "shared"
	}
	if !memory {
		return nil
	}
	// A private database belongs to one connection. Shared memory databases still
	// disappear when the last connection closes, so retain idle connections without recycling them.
	if !shared && c.MaxOpenConns != 1 {
		return errors.New("xgorm: private SQLite memory databases require max_open_conns=1")
	}
	if c.MaxIdleConns < 1 || c.ConnMaxLifetime != 0 || c.ConnMaxIdleTime != 0 {
		return errors.New("xgorm: SQLite memory databases require idle connections and disabled connection recycling")
	}
	return nil
}
