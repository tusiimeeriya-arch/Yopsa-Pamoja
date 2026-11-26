// Package db runs database transactions that carry the row-level-security
// session variables described in ARCHITECTURE §5. Every statement issued by a
// handler runs inside one of these transactions, so a policy can never read a
// stale or client-supplied identity.
package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Actor is the authenticated caller a transaction runs as.
type Actor struct {
	UserID string
	Scopes []string
}

// TxFunc is the work performed inside the transaction.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// WithActor runs fn in a transaction that sets pamoja.actor_id,
// pamoja.scope_ids and pamoja.request_id. The settings are transaction-local
// (set_config(..., true)) and are cleared when the transaction ends, so a
// pooled connection never leaks one request's identity into the next.
func WithActor(ctx context.Context, pool *pgxpool.Pool, actor Actor, requestID string, fn TxFunc) error {
	return inTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := setConfig(ctx, tx, "pamoja.actor_id", actor.UserID); err != nil {
			return err
		}
		if err := setConfig(ctx, tx, "pamoja.scope_ids", strings.Join(actor.Scopes, ",")); err != nil {
			return err
		}
		if err := setConfig(ctx, tx, "pamoja.request_id", requestID); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

// WithRequest runs fn in a transaction that carries only the correlation id.
// It is used before a session exists (login, refresh, failed-login recording),
// where the database functions themselves decide what may be read.
func WithRequest(ctx context.Context, pool *pgxpool.Pool, requestID string, fn TxFunc) error {
	return inTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := setConfig(ctx, tx, "pamoja.request_id", requestID); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func inTx(ctx context.Context, pool *pgxpool.Pool, fn TxFunc) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() {
		// Rollback after a successful commit is a no-op.
		_ = tx.Rollback(ctx)
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

func setConfig(ctx context.Context, tx pgx.Tx, key, value string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, key, value); err != nil {
		return fmt.Errorf("db: set %s: %w", key, err)
	}
	return nil
}
