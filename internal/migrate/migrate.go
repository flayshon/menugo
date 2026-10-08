// Package migrate applies versioned SQL migrations to a MariaDB database.
//
// Migrations are files named NNNNNN_description.sql (e.g.
// 000001_create_users.sql). They are applied in version order and recorded in
// the schema_migrations table. There are no down migrations: to undo a change,
// write a new migration.
//
// MariaDB cannot roll back DDL statements, so a migration that fails part way
// through is recorded as dirty and Up refuses to continue until someone has
// fixed the database by hand and deleted the dirty row. Keep each migration
// small to make that rare and easy to repair.
//
// The *sql.DB passed to Up must allow multiple statements per Exec (the
// go-sql-driver/mysql multiStatements option).
package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Migration is a single versioned SQL script.
type Migration struct {
	Version int64
	Name    string
	SQL     string
}

var filenameRX = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.sql$`)

// lockName is the MariaDB named lock that stops two processes from running
// migrations at the same time.
const lockName = "menugo_schema_migrations"

// Load reads all migrations from the root of fsys and returns them sorted by
// version. Files that don't end in .sql are ignored.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading migrations directory: %w", err)
	}

	var migrations []Migration
	seen := make(map[int64]string)

	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}

		matches := filenameRX.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("invalid migration filename %q (want NNNNNN_description.sql)", entry.Name())
		}

		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		if other, ok := seen[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %d: %q and %q", version, other, entry.Name())
		}
		seen[version] = entry.Name()

		body, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("migration %q is empty", entry.Name())
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    matches[2],
			SQL:     string(body),
		})
	}

	slices.SortFunc(migrations, func(a, b Migration) int {
		return cmp.Compare(a.Version, b.Version)
	})

	return migrations, nil
}

// Up applies every migration in fsys that hasn't been applied yet, in version
// order. It returns the migrations it applied.
func Up(ctx context.Context, db *sql.DB, fsys fs.FS) ([]Migration, error) {
	migrations, err := Load(fsys)
	if err != nil {
		return nil, err
	}

	// A named lock belongs to a connection, so everything below runs on one.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting connection: %w", err)
	}
	defer conn.Close()

	var locked sql.NullInt64
	err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", lockName).Scan(&locked)
	if err != nil {
		return nil, fmt.Errorf("acquiring migration lock: %w", err)
	}
	if locked.Int64 != 1 {
		return nil, errors.New("timed out waiting for the migration lock; is another migration running?")
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(?)", lockName)

	_, err = conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT NOT NULL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			dirty BOOLEAN NOT NULL,
			applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
		)`)
	if err != nil {
		return nil, fmt.Errorf("creating schema_migrations table: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range migrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}

		_, err := conn.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, dirty) VALUES (?, ?, TRUE)",
			m.Version, m.Name)
		if err != nil {
			return done, fmt.Errorf("recording migration %d: %w", m.Version, err)
		}

		if _, err := conn.ExecContext(ctx, m.SQL); err != nil {
			return done, fmt.Errorf("applying migration %d_%s (now marked dirty; fix the database by hand, then delete its schema_migrations row): %w", m.Version, m.Name, err)
		}

		_, err = conn.ExecContext(ctx, "UPDATE schema_migrations SET dirty = FALSE WHERE version = ?", m.Version)
		if err != nil {
			return done, fmt.Errorf("marking migration %d clean: %w", m.Version, err)
		}

		done = append(done, m)
	}

	return done, nil
}

// appliedVersions returns the versions recorded in schema_migrations, or an
// error if any of them is dirty.
func appliedVersions(ctx context.Context, conn *sql.Conn) (map[int64]struct{}, error) {
	rows, err := conn.QueryContext(ctx, "SELECT version, name, dirty FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]struct{})
	for rows.Next() {
		var (
			version int64
			name    string
			dirty   bool
		)
		if err := rows.Scan(&version, &name, &dirty); err != nil {
			return nil, fmt.Errorf("scanning schema_migrations: %w", err)
		}
		if dirty {
			return nil, fmt.Errorf("migration %d_%s is dirty: a previous run failed part way through; fix the database by hand, then delete its schema_migrations row", version, name)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}

	return applied, nil
}
