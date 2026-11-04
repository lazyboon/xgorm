package xgorm

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
)

// newPostgresConnection preserves native pgx defaults and GORM timestamp scan locations.
func newPostgresConnection(dsn string) (*driverConnection, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	// Read decoded settings only: DSN quoting and URI escaping belong to pgx.
	zone, configured := config.RuntimeParams["timezone"]
	if !configured {
		zone, configured = config.RuntimeParams["TimeZone"]
		if legacy, exists := config.RuntimeParams["time_zone"]; exists {
			if configured && zone != legacy {
				return nil, errors.New("conflicting PostgreSQL timezone aliases: TimeZone and time_zone")
			}
			zone, configured = legacy, true
		}
	}
	// Canonical timezone takes precedence. Normalize legacy aliases before sending
	// startup parameters, since PostgreSQL does not recognize time_zone.
	delete(config.RuntimeParams, "TimeZone")
	delete(config.RuntimeParams, "time_zone")
	var options []stdlib.OptionOpenDB
	if configured {
		// Supplying Conn bypasses the dialect's pool setup, including its timestamp codec.
		// Keep that behavior here so moving pool ownership does not change scanned values.
		config.RuntimeParams["timezone"] = zone
		options = append(options, stdlib.OptionAfterConnect(func(_ context.Context, connection *pgx.Conn) error {
			location, err := time.LoadLocation(zone)
			if err != nil {
				return err
			}
			connection.TypeMap().RegisterType(&pgtype.Type{
				Name:  "timestamp",
				OID:   pgtype.TimestampOID,
				Codec: &pgtype.TimestampCodec{ScanLocation: location},
			})
			return nil
		}))
	}

	pool := stdlib.OpenDB(*config, options...)
	return &driverConnection{
		dialect: postgres.New(postgres.Config{DSN: dsn, Conn: pool}),
		pool:    pool,
	}, nil
}
