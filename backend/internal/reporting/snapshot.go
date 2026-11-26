package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
)

// Snapshot is one stored ranking result. A snapshot is evidence: it is written
// once per group and period and never updated.
type Snapshot struct {
	ID            string         `json:"id"`
	GroupID       string         `json:"group_id"`
	PeriodEnd     string         `json:"period_end"`
	PolicyVersion int            `json:"policy_version"`
	Score         float64        `json:"score"`
	Components    map[string]any `json:"components"`
	RankInBranch  int            `json:"rank_in_branch"`
}

// Snapshot computes and stores this period's ranking for one group. It is
// idempotent per period: a second call returns the stored row untouched.
func (s *Service) Snapshot(ctx context.Context, tx pgx.Tx, groupID, actorID string) (Snapshot, error) {
	if err := domain.Required("group_id", groupID); err != nil {
		return Snapshot{}, err
	}

	policy, err := s.policy(ctx, tx)
	if err != nil {
		return Snapshot{}, err
	}

	periodEnd := s.Now().UTC()
	windowStart := periodEnd.AddDate(0, 0, -policy.WindowDays)

	var marks, attended int
	if err := tx.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE ma.status IN ('present', 'late'))
		 FROM pamoja.meeting_attendance ma
		 JOIN pamoja.meeting m ON m.id = ma.meeting_id
		 WHERE ma.group_id = $1::uuid AND m.scheduled_for::date BETWEEN $2::date AND $3::date`,
		groupID, windowStart.Format("2006-01-02"), periodEnd.Format("2006-01-02")).Scan(&marks, &attended); err != nil {
		return Snapshot{}, err
	}

	var meetings, confirmed int
	if err := tx.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE register_confirmed_at IS NOT NULL)
		 FROM pamoja.meeting
		 WHERE group_id = $1::uuid AND status IN ('closed', 'register_confirmed', 'amended')
		   AND scheduled_for::date BETWEEN $2::date AND $3::date`,
		groupID, windowStart.Format("2006-01-02"), periodEnd.Format("2006-01-02")).Scan(&meetings, &confirmed); err != nil {
		return Snapshot{}, err
	}

	var loans int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.loan_disbursement d
		 JOIN pamoja.loan_application a ON a.id = d.application_id
		 WHERE a.group_id = $1::uuid AND d.disbursed_on BETWEEN $2::date AND $3::date`,
		groupID, windowStart.Format("2006-01-02"), periodEnd.Format("2006-01-02")).Scan(&loans); err != nil {
		return Snapshot{}, err
	}

	var due, onTime, settled, overdue int
	if err := tx.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE i.paid_ugx >= i.amount_ugx
		              AND (SELECT max(r.paid_on) FROM pamoja.loan_repayment r
		                   WHERE r.installment_id = i.id) <= i.due_on),
		        count(*) FILTER (WHERE i.paid_ugx >= i.amount_ugx),
		        count(*) FILTER (WHERE i.paid_ugx < i.amount_ugx AND i.due_on < current_date)
		 FROM pamoja.loan_installment i
		 JOIN pamoja.loan_application a ON a.id = i.application_id
		 WHERE a.group_id = $1::uuid AND a.status = 'disbursed'
		   AND i.due_on BETWEEN $2::date AND $3::date`,
		groupID, windowStart.Format("2006-01-02"), periodEnd.Format("2006-01-02")).
		Scan(&due, &onTime, &settled, &overdue); err != nil {
		return Snapshot{}, err
	}

	metrics := Metrics{
		OnTimePct:     pct(onTime, due),
		SettledPct:    pct(settled, due),
		ArrearsPct:    pct(overdue, due),
		AttendancePct: pct(attended, marks),
		RecordsPct:    pct(confirmed, meetings),
	}
	score := ComputeScore(policy.Weights, metrics)
	eligible := loans >= policy.MinLoans && meetings >= policy.MinMeetings

	components := map[string]any{
		"on_time": metrics.OnTimePct, "settled": metrics.SettledPct,
		"arrears": metrics.ArrearsPct, "attendance": metrics.AttendancePct,
		"records": metrics.RecordsPct, "eligible": eligible,
		"loans": loans, "meetings": meetings, "installments_due": due,
	}
	encoded, err := json.Marshal(components)
	if err != nil {
		return Snapshot{}, err
	}

	var id string
	var rank int
	err = tx.QueryRow(ctx,
		`INSERT INTO pamoja.ranking_snapshot
		   (legal_entity_id, branch_id, group_id, policy_version, period_end,
		    score, components, rank_in_branch, computed_by)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2, $3::date, $4, $5::jsonb,
		        1 + (SELECT count(*) FROM pamoja.ranking_snapshot s
		             WHERE s.branch_id = g.branch_id AND s.period_end = $3::date
		               AND s.score > $4),
		        $6::uuid
		 FROM pamoja.groups g WHERE g.id = $1::uuid
		 ON CONFLICT (group_id, period_end) DO NOTHING
		 RETURNING id::text, rank_in_branch`,
		groupID, policy.Version, periodEnd.Format("2006-01-02"), score.Total, string(encoded), actorID).
		Scan(&id, &rank)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already snapshotted for this period: return the stored evidence.
		return s.LatestSnapshot(ctx, tx, groupID)
	}
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		ID: id, GroupID: groupID, PeriodEnd: periodEnd.Format("2006-01-02"),
		PolicyVersion: policy.Version, Score: score.Total, Components: components,
		RankInBranch: rank,
	}, nil
}

// LatestSnapshot returns the newest stored ranking for a group.
func (s *Service) LatestSnapshot(ctx context.Context, tx pgx.Tx, groupID string) (Snapshot, error) {
	var out Snapshot
	var raw []byte
	var period time.Time
	err := tx.QueryRow(ctx,
		`SELECT id::text, group_id::text, period_end, policy_version, score, components, rank_in_branch
		 FROM pamoja.ranking_snapshot WHERE group_id = $1::uuid
		 ORDER BY period_end DESC LIMIT 1`, groupID).
		Scan(&out.ID, &out.GroupID, &period, &out.PolicyVersion, &out.Score, &raw, &out.RankInBranch)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNoSnapshot
	}
	if err != nil {
		return Snapshot{}, err
	}
	out.PeriodEnd = period.Format("2006-01-02")
	if err := json.Unmarshal(raw, &out.Components); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

// policy reads the versioned ranking configuration.
func (s *Service) policy(ctx context.Context, tx pgx.Tx) (Policy, error) {
	var raw []byte
	var version int
	err := tx.QueryRow(ctx,
		`SELECT value, version FROM pamoja.config_value
		 WHERE key = 'ranking.policy' ORDER BY version DESC LIMIT 1`).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, err
	}
	return ParsePolicy(raw, version)
}

func pct(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return round2(float64(part) * 100 / float64(total))
}
