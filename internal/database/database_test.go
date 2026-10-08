package database_test

import (
	"context"
	"os"
	"testing"
	"time"

	"menugo.flayshon.com/internal/database"
	"menugo.flayshon.com/internal/testdb"
)

func TestParseDSNForcesSettings(t *testing.T) {
	cfg, err := database.ParseDSN("user:pw@tcp(127.0.0.1:3306)/menugo?parseTime=false&loc=Local&time_zone=%27SYSTEM%27")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ParseTime {
		t.Error("ParseTime should be forced on")
	}
	if cfg.Loc != time.UTC {
		t.Errorf("Loc = %v; want UTC", cfg.Loc)
	}
	if got := cfg.Params["time_zone"]; got != "'+00:00'" {
		t.Errorf("time_zone = %q", got)
	}
	if cfg.MultiStatements {
		t.Error("MultiStatements must stay off for the application pool")
	}
}

func TestParseDSNDoesNotLeakPassword(t *testing.T) {
	_, err := database.ParseDSN("user:secret-password@tcp(127.0.0.1:3306/broken")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got != "invalid database DSN" {
		t.Errorf("error = %q", got)
	}
}

func TestMigrateIsIdempotentAndSessionIsUTC(t *testing.T) {
	db := testdb.New(t) // already migrated once
	ctx := context.Background()

	var name, tz string
	if err := db.QueryRowContext(ctx, "SELECT DATABASE(), @@session.time_zone").Scan(&name, &tz); err != nil {
		t.Fatal(err)
	}
	if tz != "+00:00" {
		t.Errorf("session time_zone = %q; want +00:00", tz)
	}

	cfg, err := database.ParseDSN(os.Getenv("TEST_DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name

	applied, err := database.Migrate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Errorf("second run applied %d migrations; want 0", len(applied))
	}
}
