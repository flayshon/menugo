// Package testdb gives integration tests a migrated MariaDB database with no
// rows in it.
//
// Tests that call New are skipped unless TEST_DB_DSN is set. The DSN's user
// must be allowed to create and drop databases named menugo_test_* (see
// scripts/setup-db.sh); the database name in the DSN, if any, is ignored.
//
// Creating and migrating a database takes about a second, so databases are
// reused: when a test finishes, its database is emptied and handed to the
// next test. Packages using testdb should call Main from TestMain, which
// drops the databases at the end:
//
//	func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }
//
// Without it, each test gets a new database that is dropped when it ends.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"

	"menugo.flayshon.com/internal/database"
)

type testDB struct {
	name string
	db   *sql.DB
}

var (
	mu      sync.Mutex
	pooling bool      // set by Main
	idle    []*testDB // emptied databases ready for reuse
	all     []*testDB // every database created, to drop in Main
)

// Main runs the tests, then drops the databases they used.
func Main(m *testing.M) int {
	mu.Lock()
	pooling = true
	mu.Unlock()

	code := m.Run()

	mu.Lock()
	defer mu.Unlock()
	if len(all) > 0 {
		server, _, err := connectServer(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, "testdb:", err)
			return 1
		}
		defer server.Close()
		for _, tdb := range all {
			tdb.db.Close()
			if _, err := server.Exec("DROP DATABASE " + tdb.name); err != nil {
				fmt.Fprintf(os.Stderr, "testdb: dropping %s: %v\n", tdb.name, err)
				code = 1
			}
		}
	}
	return code
}

// New returns a connection pool to a migrated, empty database for the
// duration of the test. Tests using it can run in parallel: each running
// test has a database of its own.
func New(t *testing.T) *sql.DB {
	t.Helper()

	if os.Getenv("TEST_DB_DSN") == "" {
		t.Skip("TEST_DB_DSN is not set; skipping integration test")
	}

	mu.Lock()
	reuse := pooling && len(idle) > 0
	var tdb *testDB
	if reuse {
		tdb = idle[len(idle)-1]
		idle = idle[:len(idle)-1]
	}
	usePool := pooling
	mu.Unlock()

	if tdb == nil {
		var err error
		tdb, err = create(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if usePool {
			mu.Lock()
			all = append(all, tdb)
			mu.Unlock()
		}
	}

	t.Cleanup(func() {
		if !usePool {
			drop(t, tdb)
			return
		}
		if err := empty(context.Background(), tdb.db); err != nil {
			// Don't reuse a database we couldn't clean; Main drops it.
			t.Errorf("emptying test database %s: %v", tdb.name, err)
			return
		}
		mu.Lock()
		idle = append(idle, tdb)
		mu.Unlock()
	})

	return tdb.db
}

// connectServer connects to the test server without selecting a database.
func connectServer(ctx context.Context) (*sql.DB, *mysql.Config, error) {
	cfg, err := database.ParseDSN(os.Getenv("TEST_DB_DSN"))
	if err != nil {
		return nil, nil, err
	}
	serverCfg := cfg.Clone()
	serverCfg.DBName = ""
	server, err := database.Open(ctx, serverCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to test database server: %w", err)
	}
	return server, cfg, nil
}

// create makes a new, migrated database.
func create(ctx context.Context) (*testDB, error) {
	server, cfg, err := connectServer(ctx)
	if err != nil {
		return nil, err
	}
	defer server.Close()

	// rand.Text is base32 (A-Z, 2-7), so this is a safe identifier.
	name := "menugo_test_" + strings.ToLower(rand.Text()[:16])
	if _, err := server.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		return nil, fmt.Errorf("creating test database: %w", err)
	}

	cfg.DBName = name
	if _, err := database.Migrate(ctx, cfg); err != nil {
		server.ExecContext(ctx, "DROP DATABASE "+name)
		return nil, fmt.Errorf("migrating test database: %w", err)
	}

	db, err := database.Open(ctx, cfg)
	if err != nil {
		server.ExecContext(ctx, "DROP DATABASE "+name)
		return nil, fmt.Errorf("connecting to test database: %w", err)
	}
	return &testDB{name: name, db: db}, nil
}

func drop(t *testing.T, tdb *testDB) {
	tdb.db.Close()
	server, _, err := connectServer(context.Background())
	if err != nil {
		t.Error(err)
		return
	}
	defer server.Close()
	if _, err := server.Exec("DROP DATABASE " + tdb.name); err != nil {
		t.Errorf("dropping test database %s: %v", tdb.name, err)
	}
}

// empty deletes every row except the migration history. Auto-increment
// counters are not reset, so tests must not assume particular IDs.
func empty(ctx context.Context, db *sql.DB) error {
	// Foreign key checks are per session, so use one connection throughout.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND table_name <> 'schema_migrations'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SET FOREIGN_KEY_CHECKS = 1")

	for _, table := range tables {
		// Table names come from information_schema, not from input.
		if _, err := conn.ExecContext(ctx, "DELETE FROM `"+table+"`"); err != nil {
			return fmt.Errorf("emptying %s: %w", table, err)
		}
	}
	return nil
}
