// Package testdb gives integration tests a fresh, fully migrated MariaDB
// database.
//
// Tests that call New are skipped unless TEST_DB_DSN is set. The DSN's user
// must be allowed to create and drop databases named menugo_test_* (see
// scripts/setup-db.sh); the database name in the DSN, if any, is ignored.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/database"
)

// New creates a uniquely named database, applies all migrations to it and
// returns a connection pool. The database is dropped when the test finishes,
// so tests using it can run in parallel.
func New(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN is not set; skipping integration test")
	}

	cfg, err := database.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	serverCfg := cfg.Clone()
	serverCfg.DBName = ""
	server, err := database.Open(ctx, serverCfg)
	if err != nil {
		t.Fatalf("connecting to test database server: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	// rand.Text is base32 (A-Z, 2-7), so this is a safe identifier.
	name := "menugo_test_" + strings.ToLower(rand.Text()[:16])

	if _, err := server.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating test database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := server.ExecContext(context.Background(), "DROP DATABASE "+name); err != nil {
			t.Errorf("dropping test database %s: %v", name, err)
		}
	})

	cfg.DBName = name

	if _, err := database.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrating test database: %v", err)
	}

	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	// Registered after the DROP DATABASE cleanup, so it runs before it.
	t.Cleanup(func() { db.Close() })

	return db
}
