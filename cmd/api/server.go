package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// tokenCleanupInterval is how often expired tokens are deleted.
const tokenCleanupInterval = time.Hour

// serve runs the HTTP server until ctx is cancelled, then shuts it down
// gracefully: in-flight requests and background work get up to
// config.shutdownTimeout to finish.
func (app *application) serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", app.config.port),
		Handler:           app.routes(),
		IdleTimeout:       time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		ErrorLog:          slog.NewLogLogger(app.logger.Handler(), slog.LevelError),
	}

	app.background(ctx, func(ctx context.Context) { app.cleanupExpiredTokens(ctx, tokenCleanupInterval) })
	app.background(ctx, func(ctx context.Context) { app.cleanupLimiters(ctx, time.Minute) })

	serverErr := make(chan error, 1)
	go func() {
		app.logger.Info("starting server", "addr", srv.Addr, "env", app.config.env, "version", version)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
	}

	app.logger.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), app.config.shutdownTimeout)
	defer cancel()

	err := srv.Shutdown(shutdownCtx)

	// Background goroutines watch ctx, which is already cancelled.
	app.wg.Wait()

	if err != nil {
		return fmt.Errorf("shutting down server: %w", err)
	}
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	app.logger.Info("stopped server")
	return nil
}

// background runs fn in a goroutine that serve waits for before returning.
// fn must return when ctx is cancelled. A panic in fn is logged instead of
// crashing the process.
func (app *application) background(ctx context.Context, fn func(context.Context)) {
	app.wg.Go(func() {
		defer func() {
			if err := recover(); err != nil {
				app.logger.Error("panic in background goroutine", "panic", fmt.Sprint(err))
			}
		}()
		fn(ctx)
	})
}

func (app *application) cleanupExpiredTokens(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := app.models.Tokens.DeleteExpired(ctx)
			if err != nil && ctx.Err() == nil {
				app.logger.Error("deleting expired tokens", "error", err)
				continue
			}
			if n > 0 {
				app.logger.Info("deleted expired tokens", "count", n)
			}
		}
	}
}
