// Package ledger defines the LedgerAdapter contract (ADR-0008) and its working
// sandbox implementation. The Alogsys connector is a configuration-driven
// skeleton; it is never claimed to work without vendor access.
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Posting is one repayment or disbursement the core ledger must record.
type Posting struct {
	ExternalRef string
	GroupID     string
	AmountUGX   int64
	Kind        string // repayment | disbursement
}

// Balance is a ledger balance for one group and kind.
type Balance struct {
	GroupID   string
	Kind      string
	AmountUGX int64
}

// Adapter is the integration boundary. The API never talks to a vendor SDK
// directly; a failure is a posting-pending state, not a lost fact.
type Adapter interface {
	PostRepayment(ctx context.Context, tx pgx.Tx, p Posting) (externalRef string, err error)
	ConfirmPosting(ctx context.Context, tx pgx.Tx, externalRef string) (bool, error)
	GetBalances(ctx context.Context, tx pgx.Tx, groupID string) ([]Balance, error)
	ListDisbursements(ctx context.Context, tx pgx.Tx, groupID string) ([]Posting, error)
}

// Sandbox is the fully working development and test adapter. It records the
// same contract the real adapter must honour. FailRefs lets a test prove the
// posting-pending path without touching a real ledger.
type Sandbox struct {
	FailRefs map[string]bool
}

// NewSandbox returns a sandbox adapter.
func NewSandbox() *Sandbox { return &Sandbox{FailRefs: map[string]bool{}} }

// PostRepayment is idempotent on external_ref: a duplicate callback returns the
// existing reference and does not create a second ledger row.
func (s *Sandbox) PostRepayment(ctx context.Context, tx pgx.Tx, p Posting) (string, error) {
	if p.ExternalRef == "" {
		return "", errors.New("ledger: external reference is required")
	}
	if p.AmountUGX <= 0 {
		return "", errors.New("ledger: amount must be positive")
	}
	if s.FailRefs[p.ExternalRef] {
		return "", fmt.Errorf("ledger: sandbox rejected %s", p.ExternalRef)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO pamoja.sandbox_ledger (external_ref, group_id, kind, amount_ugx)
		 VALUES ($1, $2::uuid, $3, $4)
		 ON CONFLICT (external_ref) DO NOTHING`,
		p.ExternalRef, p.GroupID, p.Kind, p.AmountUGX); err != nil {
		return "", err
	}
	return p.ExternalRef, nil
}

// ConfirmPosting reports whether the sandbox ledger accepted the reference.
func (s *Sandbox) ConfirmPosting(ctx context.Context, tx pgx.Tx, externalRef string) (bool, error) {
	var status string
	err := tx.QueryRow(ctx,
		`SELECT status FROM pamoja.sandbox_ledger WHERE external_ref = $1`, externalRef).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "posted", nil
}

// GetBalances returns the sandbox balances for a group.
func (s *Sandbox) GetBalances(ctx context.Context, tx pgx.Tx, groupID string) ([]Balance, error) {
	rows, err := tx.Query(ctx,
		`SELECT group_id::text, kind, COALESCE(sum(amount_ugx), 0)
		 FROM pamoja.sandbox_ledger WHERE group_id = $1::uuid GROUP BY group_id, kind`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Balance
	for rows.Next() {
		var b Balance
		if err := rows.Scan(&b.GroupID, &b.Kind, &b.AmountUGX); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListDisbursements returns sandbox disbursements for a group.
func (s *Sandbox) ListDisbursements(ctx context.Context, tx pgx.Tx, groupID string) ([]Posting, error) {
	rows, err := tx.Query(ctx,
		`SELECT external_ref, group_id::text, amount_ugx, kind
		 FROM pamoja.sandbox_ledger WHERE group_id = $1::uuid AND kind = 'disbursement'`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Posting
	for rows.Next() {
		var p Posting
		if err := rows.Scan(&p.ExternalRef, &p.GroupID, &p.AmountUGX, &p.Kind); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Alogsys is the connector skeleton. It refuses to pretend it works until the
// vendor endpoints and credentials are supplied (BLOCKERS B-03).
type Alogsys struct {
	BaseURL string
	APIKey  string
}

// PostRepayment refuses until the vendor endpoint and credentials exist.
func (a *Alogsys) PostRepayment(context.Context, pgx.Tx, Posting) (string, error) {
	return "", errors.New("ledger: Alogsys connector is not configured; set LEDGER_ADAPTER=sandbox or supply vendor endpoints")
}

// ConfirmPosting refuses until the vendor endpoint and credentials exist.
func (a *Alogsys) ConfirmPosting(context.Context, pgx.Tx, string) (bool, error) {
	return false, errors.New("ledger: Alogsys connector is not configured")
}

// GetBalances refuses until the vendor endpoint and credentials exist.
func (a *Alogsys) GetBalances(context.Context, pgx.Tx, string) ([]Balance, error) {
	return nil, errors.New("ledger: Alogsys connector is not configured")
}

// ListDisbursements refuses until the vendor endpoint and credentials exist.
func (a *Alogsys) ListDisbursements(context.Context, pgx.Tx, string) ([]Posting, error) {
	return nil, errors.New("ledger: Alogsys connector is not configured")
}
