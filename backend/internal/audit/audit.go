// Package audit writes the append-only, hash-chained audit log described in
// ADR-0004. The chain itself is computed by a database trigger so that every
// row is linked regardless of which code path inserted it; this package
// provides the typed entry, redaction of sensitive fields, and the verifier.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Querier is the subset of pgx this package needs. Both a transaction and a
// pool satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Entry is one audited action. Identifiers are strings; empty means NULL.
type Entry struct {
	ActorUserID string
	ActorRole   string
	ScopeType   string
	ScopeID     string
	Action      string
	EntityType  string
	EntityID    string
	Before      map[string]any
	After       map[string]any
	Reason      string
	Channel     string
	IP          string
	DeviceID    string
}

// Recorder appends entries and verifies the chain.
type Recorder struct {
	q Querier
}

// New returns a Recorder backed by q.
func New(q Querier) *Recorder { return &Recorder{q: q} }

const insertSQL = `
SELECT pamoja.audit_append(
    $1::uuid, $2, $3, $4::uuid, $5, $6, $7::uuid, $8::jsonb, $9::jsonb, $10,
    $11, $12, $13, $14)`

// Append writes one entry in the caller's transaction and returns its
// sequence number. Rows go through the definer function (the API role has no
// direct INSERT on audit_log) and the chain trigger fills prev_hash and hash.
func (r *Recorder) Append(ctx context.Context, requestID string, e Entry) (int64, error) {
	before, err := encode(e.Before)
	if err != nil {
		return 0, err
	}
	after, err := encode(e.After)
	if err != nil {
		return 0, err
	}
	channel := e.Channel
	if channel == "" {
		channel = "self"
	}

	var seq int64
	err = r.q.QueryRow(ctx, insertSQL,
		nullable(e.ActorUserID),
		e.ActorRole,
		e.ScopeType,
		nullable(e.ScopeID),
		e.Action,
		e.EntityType,
		nullable(e.EntityID),
		before,
		after,
		e.Reason,
		channel,
		e.IP,
		e.DeviceID,
		requestID,
	).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("audit: append: %w", err)
	}
	return seq, nil
}

// VerifyResult is the outcome of walking the chain.
type VerifyResult struct {
	Checked     int64
	FirstBadSeq *int64
	Reason      string
}

// Intact reports whether the chain verified without a break.
func (v VerifyResult) Intact() bool { return v.FirstBadSeq == nil }

// Verify walks the chain from the first row and reports the first break.
func (r *Recorder) Verify(ctx context.Context) (VerifyResult, error) {
	var result VerifyResult
	var reason *string
	err := r.q.QueryRow(ctx,
		`SELECT checked, first_bad_seq, reason FROM pamoja.audit_verify(1)`).
		Scan(&result.Checked, &result.FirstBadSeq, &reason)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("audit: verify: %w", err)
	}
	if reason != nil {
		result.Reason = *reason
	}
	return result, nil
}

// sensitiveKeys are redacted from before/after snapshots before they reach the
// database. Names are matched case-insensitively.
var sensitiveKeys = map[string]bool{
	"password":      true,
	"password_hash": true,
	"pin":           true,
	"totp":          true,
	"totp_code":     true,
	"totp_secret":   true,
	"secret":        true,
	"secret_enc":    true,
	"access_token":  true,
	"refresh_token": true,
	"recovery_code": true,
	"national_id":   true,
	"phone":         true,
	"phone_number":  true,
}

// Redacted is the placeholder written in place of a sensitive value.
const Redacted = "[redacted]"

// Redact returns a copy of value with sensitive fields replaced. It never
// mutates the caller's map.
func Redact(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	redacted, ok := redactValue(value).(map[string]any)
	if !ok {
		return nil
	}
	return redacted
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			if sensitiveKeys[strings.ToLower(key)] {
				out[key] = Redacted
				continue
			}
			out[key] = redactValue(inner)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = redactValue(inner)
		}
		return out
	default:
		return value
	}
}

func encode(snapshot map[string]any) (any, error) {
	if snapshot == nil {
		return nil, nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("audit: encode snapshot: %w", err)
	}
	return string(raw), nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
