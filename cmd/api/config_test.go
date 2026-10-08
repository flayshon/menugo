package main

import (
	"strings"
	"testing"
	"time"
)

func getenvFrom(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(nil, getenvFrom(map[string]string{"DB_DSN": "dsn"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != 4000 || cfg.env != "development" || cfg.db.dsn != "dsn" {
		t.Errorf("got port=%d env=%q dsn=%q", cfg.port, cfg.env, cfg.db.dsn)
	}
	if cfg.auth.tokenTTL != 24*time.Hour {
		t.Errorf("tokenTTL = %v", cfg.auth.tokenTTL)
	}
	if cfg.migrate {
		t.Error("migrate should default to false")
	}
}

func TestParseConfigFlagsOverrideEnvironment(t *testing.T) {
	env := map[string]string{"DB_DSN": "dsn", "PORT": "5000", "ENV": "staging", "AUTH_TOKEN_TTL": "2h"}

	cfg, err := parseConfig([]string{"-port", "6000", "-migrate"}, getenvFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != 6000 {
		t.Errorf("port = %d; want 6000 (from flag)", cfg.port)
	}
	if cfg.env != "staging" || cfg.auth.tokenTTL != 2*time.Hour {
		t.Errorf("env = %q, tokenTTL = %v; want values from environment", cfg.env, cfg.auth.tokenTTL)
	}
	if !cfg.migrate {
		t.Error("migrate should be true")
	}
}

func TestParseConfigRejectsInvalidSettings(t *testing.T) {
	env := map[string]string{
		"PORT":              "http",
		"ENV":               "prod",
		"DB_MAX_OPEN_CONNS": "0",
		"AUTH_TOKEN_TTL":    "forever",
	}

	_, err := parseConfig(nil, getenvFrom(env))
	if err == nil {
		t.Fatal("expected an error")
	}

	// Every problem should be reported at once.
	for _, want := range []string{"PORT must be an integer", "env must be one of", "DB_DSN must be set", "max open connections", "AUTH_TOKEN_TTL must be a duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}
