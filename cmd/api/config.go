package main

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"strconv"
	"time"
)

type config struct {
	port    int
	env     string
	migrate bool
	db      struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  time.Duration
	}
	auth struct {
		tokenTTL time.Duration
	}
	limiter struct {
		enabled bool
		rps     float64
		burst   int
	}
	// trustProxyHeaders makes the client IP the last address in
	// X-Forwarded-For, as appended by a reverse proxy in front of the API.
	// Only enable it behind such a proxy: otherwise clients can spoof it.
	trustProxyHeaders bool
	shutdownTimeout   time.Duration
}

var environments = []string{"development", "staging", "production"}

// parseConfig reads configuration from environment variables, which
// command-line flags can override. It returns an error describing every
// invalid setting, so the application can refuse to start.
func parseConfig(args []string, getenv func(string) string) (config, error) {
	var cfg config
	var errs []error

	env := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	envInt := func(key string, fallback int) int {
		s := getenv(key)
		if s == "" {
			return fallback
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be an integer", key))
		}
		return n
	}
	envFloat := func(key string, fallback float64) float64 {
		s := getenv(key)
		if s == "" {
			return fallback
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be a number", key))
		}
		return f
	}
	envBool := func(key string, fallback bool) bool {
		s := getenv(key)
		if s == "" {
			return fallback
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be true or false", key))
		}
		return b
	}
	envDuration := func(key string, fallback time.Duration) time.Duration {
		s := getenv(key)
		if s == "" {
			return fallback
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be a duration like 30s or 15m", key))
		}
		return d
	}

	fs := flag.NewFlagSet("api", flag.ContinueOnError)

	fs.IntVar(&cfg.port, "port", envInt("PORT", 4000), "API server port (PORT)")
	fs.StringVar(&cfg.env, "env", env("ENV", "development"), "environment: development|staging|production (ENV)")
	fs.BoolVar(&cfg.migrate, "migrate", false, "apply database migrations and exit")

	fs.StringVar(&cfg.db.dsn, "db-dsn", getenv("DB_DSN"), "MariaDB DSN (DB_DSN)")
	fs.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", envInt("DB_MAX_OPEN_CONNS", 25), "maximum open connections (DB_MAX_OPEN_CONNS)")
	fs.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", envInt("DB_MAX_IDLE_CONNS", 25), "maximum idle connections (DB_MAX_IDLE_CONNS)")
	fs.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", envDuration("DB_MAX_IDLE_TIME", 15*time.Minute), "maximum connection idle time (DB_MAX_IDLE_TIME)")

	fs.DurationVar(&cfg.auth.tokenTTL, "auth-token-ttl", envDuration("AUTH_TOKEN_TTL", 24*time.Hour), "authentication token lifetime (AUTH_TOKEN_TTL)")
	fs.BoolVar(&cfg.limiter.enabled, "limiter-enabled", envBool("LIMITER_ENABLED", true), "enable rate limiting (LIMITER_ENABLED)")
	fs.Float64Var(&cfg.limiter.rps, "limiter-rps", envFloat("LIMITER_RPS", 20), "requests per second per client IP (LIMITER_RPS)")
	fs.IntVar(&cfg.limiter.burst, "limiter-burst", envInt("LIMITER_BURST", 40), "request burst per client IP (LIMITER_BURST)")
	fs.BoolVar(&cfg.trustProxyHeaders, "trust-proxy-headers", envBool("TRUST_PROXY_HEADERS", false), "take client IPs from X-Forwarded-For (TRUST_PROXY_HEADERS)")
	fs.DurationVar(&cfg.shutdownTimeout, "shutdown-timeout", envDuration("SHUTDOWN_TIMEOUT", 30*time.Second), "graceful shutdown timeout (SHUTDOWN_TIMEOUT)")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if cfg.port < 1 || cfg.port > 65535 {
		errs = append(errs, errors.New("port must be between 1 and 65535"))
	}
	if !slices.Contains(environments, cfg.env) {
		errs = append(errs, fmt.Errorf("env must be one of %v", environments))
	}
	if cfg.db.dsn == "" {
		errs = append(errs, errors.New("DB_DSN must be set"))
	}
	if cfg.db.maxOpenConns < 1 {
		errs = append(errs, errors.New("db max open connections must be at least 1"))
	}
	if cfg.db.maxIdleConns < 0 || cfg.db.maxIdleConns > cfg.db.maxOpenConns {
		errs = append(errs, errors.New("db max idle connections must be between 0 and the max open connections"))
	}
	if cfg.db.maxIdleTime <= 0 {
		errs = append(errs, errors.New("db max idle time must be positive"))
	}
	if cfg.auth.tokenTTL < time.Minute {
		errs = append(errs, errors.New("auth token TTL must be at least 1m"))
	}
	if cfg.limiter.enabled && (cfg.limiter.rps <= 0 || cfg.limiter.burst < 1) {
		errs = append(errs, errors.New("limiter rps must be positive and burst at least 1"))
	}
	if cfg.shutdownTimeout <= 0 {
		errs = append(errs, errors.New("shutdown timeout must be positive"))
	}

	if err := errors.Join(errs...); err != nil {
		return config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}
