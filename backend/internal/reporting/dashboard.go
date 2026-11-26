package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
)

// Service builds dashboards, rankings and exports inside the caller's
// transaction so row-level security decides what can be read.
type Service struct {
	Now func() time.Time
}

// New returns a reporting service.
func New() *Service { return &Service{Now: time.Now} }

// Card is one dashboard tile. Value is numeric and Unit says how to read it.
type Card struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"` // count | ugx | percent
	DrillDown string  `json:"drill_down,omitempty"`
}

// Dashboard is the group's management summary.
type Dashboard struct {
	GroupID    string    `json:"group_id"`
	AsOf       time.Time `json:"as_of"`
	WindowDays int       `json:"window_days"`
	Cards      []Card    `json:"cards"`
}

// windowDays is the dashboard attendance window.
const windowDays = 90

// Dashboard computes every card in one pass. A missing figure is zero, never
// inferred from another period.
func (s *Service) Dashboard(ctx context.Context, tx pgx.Tx, groupID string) (Dashboard, error) {
	if err := domain.Required("group_id", groupID); err != nil {
		return Dashboard{}, err
	}

	var members int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.membership WHERE group_id = $1::uuid AND status = 'active'`,
		groupID).Scan(&members); err != nil {
		return Dashboard{}, err
	}

	var marks, attended int
	if err := tx.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE ma.status IN ('present', 'late'))
		 FROM pamoja.meeting_attendance ma
		 JOIN pamoja.meeting m ON m.id = ma.meeting_id
		 WHERE ma.group_id = $1::uuid
		   AND m.scheduled_for >= now() - ($2::int * interval '1 day')`,
		groupID, windowDays).Scan(&marks, &attended); err != nil {
		return Dashboard{}, err
	}
	attendancePct := 0.0
	if marks > 0 {
		attendancePct = float64(attended) * 100 / float64(marks)
	}

	var savings int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(sum(amount_ugx), 0) FROM pamoja.payment_entry
		 WHERE group_id = $1::uuid AND category = 'savings' AND status = 'posted'`,
		groupID).Scan(&savings); err != nil {
		return Dashboard{}, err
	}

	var outstanding, arrears int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(sum(i.amount_ugx - i.paid_ugx), 0),
		        COALESCE(sum(i.amount_ugx - i.paid_ugx) FILTER (WHERE i.due_on < current_date), 0)
		 FROM pamoja.loan_installment i
		 JOIN pamoja.loan_application a ON a.id = i.application_id
		 WHERE a.group_id = $1::uuid AND a.status = 'disbursed' AND i.status <> 'paid'`,
		groupID).Scan(&outstanding, &arrears); err != nil {
		return Dashboard{}, err
	}

	var pendingVerifications int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.group_submission
		 WHERE group_id = $1::uuid AND status = 'under_review'`, groupID).Scan(&pendingVerifications); err != nil {
		return Dashboard{}, err
	}

	var exceptions int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.finance_exception
		 WHERE group_id = $1::uuid AND status <> 'resolved'`, groupID).Scan(&exceptions); err != nil {
		return Dashboard{}, err
	}

	return Dashboard{
		GroupID: groupID, AsOf: s.Now().UTC(), WindowDays: windowDays,
		Cards: []Card{
			{Key: "members", Label: "Active members", Value: float64(members), Unit: "count", DrillDown: "members"},
			{Key: "attendance_rate", Label: "Attendance (90 days)", Value: round2(attendancePct), Unit: "percent", DrillDown: "attendance"},
			{Key: "savings_ugx", Label: "Savings posted", Value: float64(savings), Unit: "ugx", DrillDown: "savings"},
			{Key: "loans_outstanding_ugx", Label: "Loans outstanding", Value: float64(outstanding), Unit: "ugx", DrillDown: "loans"},
			{Key: "arrears_ugx", Label: "Arrears", Value: float64(arrears), Unit: "ugx", DrillDown: "arrears"},
			{Key: "pending_verifications", Label: "Submissions awaiting verification", Value: float64(pendingVerifications), Unit: "count", DrillDown: "pending_verifications"},
			{Key: "finance_exceptions", Label: "Open finance exceptions", Value: float64(exceptions), Unit: "count", DrillDown: "finance_exceptions"},
		},
	}, nil
}

// drillDownQueries map a card to the rows behind it. Every query is scoped by
// group, so a drill-down cannot widen what the dashboard showed.
var drillDownQueries = map[string]string{
	"members": `
		SELECT mem.id::text AS member_id, mem.full_name, ms.member_no, ms.joined_on::text
		FROM pamoja.membership ms
		JOIN pamoja.member mem ON mem.id = ms.member_id
		WHERE ms.group_id = $1::uuid AND ms.status = 'active'
		ORDER BY mem.full_name LIMIT $2`,
	"attendance": `
		SELECT m.scheduled_for::date::text AS meeting, ma.status, mem.full_name
		FROM pamoja.meeting_attendance ma
		JOIN pamoja.meeting m ON m.id = ma.meeting_id
		JOIN pamoja.member mem ON mem.id = ma.member_id
		WHERE ma.group_id = $1::uuid
		  AND m.scheduled_for >= now() - ($3::int * interval '1 day')
		ORDER BY m.scheduled_for DESC, mem.full_name LIMIT $2`,
	"savings": `
		SELECT pe.paid_on::text, mem.full_name, pe.amount_ugx, pe.channel, pe.status
		FROM pamoja.payment_entry pe
		JOIN pamoja.member mem ON mem.id = pe.member_id
		WHERE pe.group_id = $1::uuid AND pe.category = 'savings' AND pe.status = 'posted'
		ORDER BY pe.paid_on DESC, pe.created_at DESC LIMIT $2`,
	"loans": `
		SELECT a.id::text AS application_id, p.code AS product, a.loan_kind, a.status,
		       i.due_on::text, i.amount_ugx, i.paid_ugx, i.status AS installment_status
		FROM pamoja.loan_installment i
		JOIN pamoja.loan_application a ON a.id = i.application_id
		JOIN pamoja.loan_product p ON p.id = a.product_id
		WHERE a.group_id = $1::uuid AND a.status = 'disbursed' AND i.status <> 'paid'
		ORDER BY i.due_on LIMIT $2`,
	"arrears": `
		SELECT a.id::text AS application_id, p.code AS product, i.due_on::text,
		       i.amount_ugx, i.paid_ugx, (i.amount_ugx - i.paid_ugx) AS overdue_ugx
		FROM pamoja.loan_installment i
		JOIN pamoja.loan_application a ON a.id = i.application_id
		JOIN pamoja.loan_product p ON p.id = a.product_id
		WHERE a.group_id = $1::uuid AND a.status = 'disbursed'
		  AND i.status <> 'paid' AND i.due_on < current_date
		ORDER BY i.due_on LIMIT $2`,
	"pending_verifications": `
		SELECT id::text, period_start::text, period_end::text, declared_total_ugx,
		       entry_count, status
		FROM pamoja.group_submission
		WHERE group_id = $1::uuid AND status = 'under_review'
		ORDER BY period_end DESC LIMIT $2`,
	"finance_exceptions": `
		SELECT id::text, kind, status, due_on::text, resolution_note, created_at::text
		FROM pamoja.finance_exception
		WHERE group_id = $1::uuid AND status <> 'resolved'
		ORDER BY created_at DESC LIMIT $2`,
}

// DrillDown returns the rows behind one card.
func (s *Service) DrillDown(ctx context.Context, tx pgx.Tx, groupID, card string, limit int) ([]map[string]any, error) {
	query, ok := drillDownQueries[card]
	if !ok {
		return nil, domain.Invalid("card", "is not a dashboard card with a drill-down")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{groupID, limit}
	if card == "attendance" {
		args = append(args, windowDays)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRows(rows)
}

// collectRows turns a result set into JSON-ready maps.
func collectRows(rows pgx.Rows) ([]map[string]any, error) {
	fields := rows.FieldDescriptions()
	out := []map[string]any{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(fields))
		for i, f := range fields {
			row[string(f.Name)] = values[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ErrNoSnapshot reports that a group has no ranking snapshot yet.
var ErrNoSnapshot = errors.New("reporting: no ranking snapshot")
