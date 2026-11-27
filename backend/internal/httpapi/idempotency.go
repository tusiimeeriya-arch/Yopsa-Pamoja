package httpapi

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
)

// beginIdempotency reserves the key or returns the stored response. A nil body
// means "proceed"; a non-nil body is a replay.
func beginIdempotency(ctx context.Context, tx pgx.Tx, actorID, key, route, requestHash string) (int, []byte, error) {
	var storedHash string
	var status *int
	var body []byte
	err := tx.QueryRow(ctx,
		`SELECT request_hash, response_status, response_body_redacted
		 FROM pamoja.idempotency_key
		 WHERE actor_user_id = $1::uuid AND key = $2
		 FOR UPDATE`,
		actorID, key).Scan(&storedHash, &status, &body)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx,
			`INSERT INTO pamoja.idempotency_key
			    (actor_user_id, key, route, request_hash, expires_at)
			 VALUES ($1::uuid, $2, $3, $4, now() + interval '24 hours')`,
			actorID, key, route, requestHash)
		if err != nil {
			return 0, nil, err
		}
		return 0, nil, nil
	case err != nil:
		return 0, nil, err
	}

	if storedHash != requestHash {
		return 0, nil, domain.ErrIdempotencyConflict
	}
	if status == nil || body == nil {
		// The first request has not completed. The row lock above means a
		// concurrent duplicate waits here; if it still sees no response the
		// first attempt rolled back, so treat it as a conflict rather than
		// inventing a response.
		return 0, nil, domain.ErrConflict
	}
	return *status, body, nil
}

func finishIdempotency(ctx context.Context, tx pgx.Tx, actorID, key string, status int, body []byte) error {
	_, err := tx.Exec(ctx,
		`UPDATE pamoja.idempotency_key
		 SET response_status = $3, response_body_redacted = $4::jsonb
		 WHERE actor_user_id = $1::uuid AND key = $2`,
		actorID, key, status, string(body))
	return err
}
