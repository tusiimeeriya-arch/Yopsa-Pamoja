// Command worker runs asynchronous jobs (scheduled snapshots, reminders,
// reconciliation checks, integration delivery).
//
// P0 ships the process skeleton only: it connects to PostgreSQL, verifies the
// connection, logs readiness and shuts down cleanly. The PostgreSQL-backed job
// queue (river) and the first handlers arrive in P1 with the platform core.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/platform/config"
	"github.com/yopsa/pamoja/backend/internal/platform/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log, err := logging.New(cfg.LogLevel, string(cfg.AppEnv))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("database not reachable: %w", err)
	}

	log.Info("worker ready", "queue", "not-configured", "note", "job handlers arrive in P1")
	<-ctx.Done()
	log.Info("worker stopped")
	return nil
}
