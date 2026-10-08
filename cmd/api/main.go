package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/database"
)

const version = "0.1.0"

type application struct {
	config config
	logger *slog.Logger
	db     *sql.DB
	models data.Models
	wg     sync.WaitGroup // tracks background goroutines for graceful shutdown
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run is main without the process-level concerns, so it can be tested.
func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	cfg, err := parseConfig(args, getenv)
	if err != nil {
		return err
	}

	logger := newLogger(stdout, cfg.env)

	dbCfg, err := database.ParseDSN(cfg.db.dsn)
	if err != nil {
		return err
	}

	if cfg.migrate {
		applied, err := database.Migrate(ctx, dbCfg)
		for _, m := range applied {
			logger.Info("applied migration", "version", m.Version, "name", m.Name)
		}
		if err != nil {
			return err
		}
		logger.Info("database is up to date", "applied", len(applied))
		return nil
	}

	db, err := database.Open(ctx, dbCfg)
	if err != nil {
		return err
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)
	db.SetConnMaxIdleTime(cfg.db.maxIdleTime)

	logger.Info("database connection pool established")

	app := &application{
		config: cfg,
		logger: logger,
		db:     db,
		models: data.NewModels(db),
	}

	return app.serve(ctx)
}

func newLogger(w io.Writer, env string) *slog.Logger {
	if env == "development" {
		return slog.New(slog.NewTextHandler(w, nil))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}
