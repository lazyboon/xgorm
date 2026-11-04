package xgorm

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
	"time"
)

func sqliteConfig(alias string) Config {
	return Config{Alias: alias, Driver: DriverSQLite, DSN: ":memory:", PingTimeout: 1, MaxOpenConns: 1, MaxIdleConns: 1}
}
func TestDriverValidate(t *testing.T) {
	t.Parallel()
	for _, d := range []Driver{DriverMySQL, DriverPostgres, DriverSQLite, DriverSQLServer} {
		if err := d.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := Driver("unknown").Validate(); err == nil {
		t.Fatal("unsupported driver accepted")
	}
}
func TestConfigValidate(t *testing.T) {
	t.Parallel()
	type validationCase struct {
		name   string
		change func(*Config)
	}
	for _, tc := range []validationCase{
		{"blank alias", func(c *Config) { c.Alias = " " }}, {"alias spaces", func(c *Config) { c.Alias = " main" }}, {"empty DSN", func(c *Config) { c.DSN = " " }}, {"null DSN", func(c *Config) { c.DSN = "a\x00b" }},
		{"ping zero", func(c *Config) { c.PingTimeout = 0 }}, {"ping overflow", func(c *Config) { c.PingTimeout = int((1<<63-1)/time.Second) + 1 }}, {"open zero", func(c *Config) { c.MaxOpenConns = 0 }}, {"idle negative", func(c *Config) { c.MaxIdleConns = -1 }}, {"idle above open", func(c *Config) { c.MaxIdleConns = 2 }}, {"lifetime negative", func(c *Config) { c.ConnMaxLifetime = -1 }}, {"idle time negative", func(c *Config) { c.ConnMaxIdleTime = -1 }},
		{"GORM dialect", func(c *Config) { c.GORM = &gorm.Config{Dialector: sqlite.Open(":memory:")} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := sqliteConfig("main")
			tc.change(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
func TestConfigSQLiteMemoryLimits(t *testing.T) {
	t.Parallel()
	type memoryCase struct {
		name, dsn                      string
		open, idle, lifetime, idleTime int
		valid                          bool
	}
	for _, tc := range []memoryCase{
		{"private", ":memory:", 1, 1, 0, 0, true}, {"private multiple", ":memory:", 2, 1, 0, 0, false}, {"private no idle", ":memory:", 1, 0, 0, 0, false}, {"private lifetime", ":memory:", 1, 1, 1, 0, false}, {"private idle time", ":memory:", 1, 1, 0, 1, false},
		{"shared", "file:shared?mode=memory&cache=shared", 4, 2, 0, 0, true}, {"shared no idle", "file:shared?mode=memory&cache=shared", 4, 0, 0, 0, false}, {"encoded private", "file:%3Amemory%3A", 2, 1, 0, 0, false}, {"URI fragment", "file::memory:#?cache=shared", 2, 1, 0, 0, false}, {"ordinary fragment", "name#part?mode=memory", 2, 0, 1, 1, true},
		{"file", "test.db", 4, 0, 10, 10, true}, {"ordinary query not memory", "test.db?mode=memory", 4, 0, 10, 10, true}, {"repeated mode", "file:test?mode=memory&mode=ro", 1, 1, 0, 0, false}, {"repeated cache", "file:test?cache=shared&cache=private", 1, 1, 0, 0, false}, {"bad query", "file:test?mode=%xx", 1, 1, 0, 0, false}, {"bad filename escape", "file:%xx", 1, 1, 0, 0, false}, {"null filename", "file:%00", 1, 1, 0, 0, false}, {"null query key", "file:test?%00=1", 1, 1, 0, 0, false}, {"null query value", "file:test?mode=%00", 1, 1, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := sqliteConfig("main")
			config.DSN = tc.dsn
			config.MaxOpenConns = tc.open
			config.MaxIdleConns = tc.idle
			config.ConnMaxLifetime = tc.lifetime
			config.ConnMaxIdleTime = tc.idleTime
			if err := config.Validate(); (err == nil) != tc.valid {
				t.Fatalf("Validate = %v, valid=%v", err, tc.valid)
			}
		})
	}
}
