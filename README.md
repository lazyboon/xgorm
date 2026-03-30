# xgorm

`xgorm` is a lightweight helper layer on top of [GORM](https://gorm.io/) for:

- managing multiple database connections by alias
- applying safe connection-pool defaults
- defining and syncing MySQL/PostgreSQL indexes in code

## Features

- Multi-DB registry with `Init`, `Open`, `Get`, `Close`, `CloseAll`
- Supported drivers: MySQL, PostgreSQL, SQLite, SQL Server
- Ping check with timeout on startup
- Fluent index builders:
  - `MysqlIndex` + `MysqlIndexManager`
  - `PostgresIndex` + `PostgresIndexManager`
- Ghost index cleanup support for managed index names

## Install

```bash
go get github.com/lazyboon/xgorm
```

## Quick Start

```go
package main

import (
	"log"

	"github.com/lazyboon/xgorm"
)

func main() {
	err := xgorm.Init([]*xgorm.Config{
		{
			Alias:  "main",
			Driver: xgorm.DriverPostgres,
			DSN:    "host=127.0.0.1 user=postgres password=postgres dbname=app port=5432 sslmode=disable",
		},
		{
			Alias:  "analytics",
			Driver: xgorm.DriverMySQL,
			DSN:    "user:pass@tcp(127.0.0.1:3306)/analytics?parseTime=True",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer xgorm.CloseAll()

	db := xgorm.Get("main")
	if db == nil {
		log.Fatal("missing db alias: main")
	}

	_ = db
}
```

## Configuration

`Config` fields:

- `Alias` (required): unique instance key
- `Driver` (optional): defaults to `mysql`
- `DSN` (required): driver-specific connection string
- `MaxIdleConn`, `MaxOpenConn`, `ConnMaxLifetime`, `ConnMaxIdleTime`
- `PingTimeout`
- `Debug`
- `GormConfig`

## Supported Databases

- MySQL
- PostgreSQL
- SQLite
- SQL Server

## Index Management

### MySQL

```go
idx := xgorm.NewMysqlIndex().
	Model(&User{}).
	Fields("email").
	Unique(true)

mgr := xgorm.NewMysqlIndexManager(db, []*xgorm.MysqlIndex{idx})
if err := mgr.Create(); err != nil {
	return err
}

// Optional: remove stale managed indexes
if err := mgr.CleanupGhost(); err != nil {
	return err
}
```

### PostgreSQL

```go
idx := xgorm.NewPostgresIndex().
	Model(&User{}).
	Fields("email").
	Method(xgorm.PostgresIndexBTree).
	Unique(true).
	IfNotExists(true)

mgr := xgorm.NewPostgresIndexManager(db, []*xgorm.PostgresIndex{idx})
if err := mgr.Create(); err != nil {
	return err
}

// Optional: remove stale managed indexes (only managed prefixes are targeted)
if err := mgr.CleanupGhost(); err != nil {
	return err
}
```

## Notes

- `PostgresIndex.Unique(true)` is only valid with `BTREE`.
- Always review `CleanupGhost()` behavior in staging before production rollout.
- `xgorm` does not run migrations for table/schema creation.

## License

MIT. See [LICENSE](./LICENSE).
