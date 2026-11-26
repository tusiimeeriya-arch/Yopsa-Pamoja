// Command auditverify walks the append-only audit hash chain and reports the
// first break. It exits non-zero when the chain is broken, so a scheduled job
// or an operator can alert on it (ADR-0004, TEST_PLAN T-AUD-02).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/platform/config"
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	result, err := audit.New(pool).Verify(ctx)
	if err != nil {
		return err
	}
	if !result.Intact() {
		return fmt.Errorf("audit chain broken at seq %d: %s", *result.FirstBadSeq, result.Reason)
	}

	fmt.Printf("audit chain intact: %d rows verified\n", result.Checked)
	return nil
}
