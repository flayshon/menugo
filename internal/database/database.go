// Package database opens MariaDB connections with the settings the
// application relies on, and applies migrations.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"

	"menugo.flayshon.com/internal/migrate"
	"menugo.flayshon.com/migrations"
)

// ParseDSN parses dsn and forces the connection settings the application
// relies on, whatever the DSN says:
//
//   - DATETIME columns are scanned into time.Time, interpreted as UTC.
//   - The session time zone is UTC, so CURRENT_TIMESTAMP() is UTC too.
//   - Strict SQL mode, so bad data is rejected instead of silently truncated.
//   - utf8mb4 with a case-insensitive Unicode collation.
func ParseDSN(dsn string) (*mysql.Config, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		// Don't wrap: the driver's error can include the DSN, and with it
		// the password.
		return nil, errors.New("invalid database DSN")
	}

	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Collation = "utf8mb4_unicode_ci"
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["time_zone"] = "'+00:00'"
	cfg.Params["sql_mode"] = "'TRADITIONAL'"

	return cfg, nil
}

// Open opens a connection pool for cfg and checks that the database is
// reachable.
func Open(ctx context.Context, cfg *mysql.Config) (*sql.DB, error) {
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating connector: %w", err)
	}

	db := sql.OpenDB(connector)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return db, nil
}

// Migrate applies all pending migrations to the database cfg points at. It
// uses its own short-lived connection because migration files contain several
// statements, which the application's pool deliberately doesn't allow.
func Migrate(ctx context.Context, cfg *mysql.Config) ([]migrate.Migration, error) {
	cfg = cfg.Clone()
	cfg.MultiStatements = true

	db, err := Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	return migrate.Up(ctx, db, migrations.FS)
}
