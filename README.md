# xgorm

A named GORM database registry with explicit pool ownership and bounded startup checks. Supports MySQL, PostgreSQL, SQLite, and SQL Server.

## Install

Requires Go 1.27.3 or later. SQLite uses `go-sqlite3` and requires CGO and a C compiler.

```sh
go get github.com/lazyboon/xgorm
```

## Start a database

```go
package main

import (
    "context"
    "log"

    "github.com/lazyboon/xgorm"
)

func main() {
    config := xgorm.Config{
        Alias: "main",
		Driver: xgorm.DriverSQLite,
		DSN: ":memory:",
        PingTimeout: 5,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
    }
    if err := xgorm.Init(context.Background(), []xgorm.Config{config}); err != nil {
        log.Fatal(err)
    }
    defer xgorm.CloseAll()

    db := xgorm.Get("main")
    var value int
    if err := db.WithContext(context.Background()).Raw("SELECT 1").Scan(&value).Error; err != nil {
        log.Print(err)
    }
}
```

See the executable `ExampleInit` for SQLite CRUD and a transaction. Schema creation and migrations belong to the caller; initialization never runs migrations or manages indexes.

## Connection settings

`Alias`, `Driver`, `DSN`, `PingTimeout`, and `MaxOpenConns` are required. Aliases must be unique and have no surrounding whitespace. Time settings use integer **seconds**. `MaxIdleConns` may be zero, must not be negative, and cannot exceed `MaxOpenConns`. `ConnMaxLifetime` and `ConnMaxIdleTime` are non-negative; zero disables recycling.

DSNs use each native driver's format. xgorm does not build DSNs, escape credentials, or add TLS settings:

| Driver | DSN example |
| --- | --- |
| `DriverMySQL` | `user:password@tcp(localhost:3306)/example?parseTime=true` |
| `DriverPostgres` | `host=localhost user=example dbname=example sslmode=require timezone=UTC` |
| `DriverSQLite` | `file:example.db` |
| `DriverSQLServer` | `sqlserver://user:password@localhost:1433?database=example` |

Choose TLS and authentication options for your environment. DSNs can contain secrets; avoid logging them.

Private SQLite memory databases (`:memory:` or an equivalent `file:` URI) require `MaxOpenConns=1`. Every memory database, including `file:example?mode=memory&cache=shared`, requires at least one idle connection and both recycling limits set to zero. Shared memory URIs allow multiple connections. File databases do not have these memory restrictions.

PostgreSQL DSNs are parsed by pgx, including quoted keyword values and percent-encoded URI values. An explicit `timezone` also controls GORM timestamp scanning. Legacy `TimeZone` and `time_zone` aliases are normalized; conflicting legacy values fail unless canonical `timezone` is present. Canonical `timezone` takes precedence. Explicit locations must be loadable by Go's time package.

MySQL initialization retains the GORM driver's version probe and uses its result for dialect behavior. The startup wrapper supplies the startup context to the driver's background-context version query. Driver upgrades that change that probe should be reviewed and tested; prepared statements and transactions still use the original SQL pool.

## Lifecycle and ownership

- `Init(ctx, []Config)` validates the whole batch, opens pools, initializes GORM, checks connectivity, and publishes the batch atomically. Failure closes pools created by that call and preserves the registry.
- `ctx` and each connection's `PingTimeout` bound context-aware startup work. Driver operations or plugins that ignore context can exceed that deadline. Successful databases do not retain the startup context; use `db.WithContext(requestCtx)` for runtime work.
- `Get(alias)` returns a shared `*gorm.DB`, or nil when absent. Use `WithContext` or `Session` for request settings. Do not mutate the shared root or close its pool directly.
- Stop database consumers before `Close(alias)` or `CloseAll()`. They remove aliases and close owned pools, retaining aliases in errors. `CloseAll` attempts every close. An absent alias closes successfully.
- Initialize during application startup. Do not overlap initialization with registry shutdown. The registry is process-wide; libraries sharing a process must coordinate aliases and shutdown.

`Config.GORM` accepts native GORM settings such as logging, naming, prepared statements, and plugins. It must not supply `ConnPool` or `Dialector`. The configuration is copied, including its plugin and clause maps; other referenced values may remain shared and must be safe for their use. Plugins must retain the owned SQL pool and wrappers must expose it through `gorm.GetDBConnector`. Automatic GORM ping is replaced by the bounded startup check. `Debug` enables SQL info logging.

## Migrating from earlier v1 releases

This update retains the v1 module path but changes APIs:

- Keep the `github.com/lazyboon/xgorm` import path and upgrade the Go toolchain.
- Replace `Init([]*Config)` with `Init(ctx, []Config)` and supply explicit driver, pool limits, and positive ping timeout.
- Replace pointer `*uint` pool and timeout fields with explicit `int` values in seconds. Rename `MaxOpenConn` / `MaxIdleConn` to `MaxOpenConns` / `MaxIdleConns`, and `GormConfig` to `GORM`. Update JSON keys to `max_open_conns` and `max_idle_conns`. `FillDefaults` is removed.
- The former exported `Open` helper is removed. Use `Init` and `Get`; this version has no exported `Open(ctx)` API.
- MySQL/PostgreSQL index builders and index managers are removed. Move schema and index migrations into application-managed migrations.
- Handle errors returned by initialization and shutdown, and coordinate shared registry ownership.

## Test

```sh
CGO_ENABLED=1 go test -race ./...
go vet ./...
```

Tests use temporary SQLite databases, fake SQL connections, and local ephemeral listeners. No database service or Docker is needed. Native database behavior beyond these fixtures should also be checked against your supported server versions.

## License

[MIT](LICENSE).
