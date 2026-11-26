// Package offline replays a device's queued operations through the server.
// The server's clock and its records win: an operation that no longer fits the
// server's state is reported as a conflict, never forced through.
package offline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/payments"
)

// Operation kinds this build can replay.
const (
	KindAttendanceCheckIn = "attendance.check_in"
	KindPaymentEntry      = "payment.entry.create"
)

// MaxOperations bounds one batch.
const MaxOperations = 200

// Service replays offline batches.
type Service struct {
	Payments *payments.Service
	Now      func() time.Time
}

// New returns an offline sync service.
func New(p *payments.Service) *Service {
	if p == nil {
		p = payments.New(nil)
	}
	return &Service{Payments: p, Now: time.Now}
}

// OperationInput is one queued change.
type OperationInput struct {
	Seq             int             `json:"seq"`
	Kind            string          `json:"kind"`
	IdempotencyKey  string          `json:"idempotency_key"`
	ClientCreatedAt time.Time       `json:"client_created_at"`
	Payload         json.RawMessage `json:"payload"`
}

// SubmitInput is one device batch.
type SubmitInput struct {
	GroupID    string           `json:"-"`
	ClientID   string           `json:"client_id"`
	ClientTime time.Time        `json:"client_time"`
	ActorID    string           `json:"-"`
	Operations []OperationInput `json:"operations"`
}

// OperationResult is what happened to one queued change.
type OperationResult struct {
	Seq            int             `json:"seq"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	ConflictReason string          `json:"conflict_reason,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
}

// Result is the batch outcome, including the server time the client should
// adopt.
type Result struct {
	BatchID    string            `json:"batch_id"`
	ServerTime time.Time         `json:"server_time"`
	SkewMS     int64             `json:"skew_ms"`
	Applied    int               `json:"applied_count"`
	Conflicts  int               `json:"conflict_count"`
	Status     string            `json:"status"`
	Operations []OperationResult `json:"operations"`
}

// SkewMS reports how far the client clock is ahead of the server (negative
// means behind). The client must correct its clock by this amount.
func SkewMS(clientTime, serverTime time.Time) int64 {
	return clientTime.Sub(serverTime).Milliseconds()
}

// BatchStatus is completed only when every operation applied.
func BatchStatus(_, conflicts int) string {
	if conflicts > 0 {
		return "completed_with_conflicts"
	}
	return "completed"
}

// ValidateOperations checks the batch shape before anything is written.
func ValidateOperations(ops []OperationInput) error {
	if len(ops) == 0 {
		return domain.Invalid("operations", "is required")
	}
	if len(ops) > MaxOperations {
		return domain.Invalid("operations", "exceeds 200 operations")
	}
	seenSeq := map[int]bool{}
	seenKey := map[string]bool{}
	for _, op := range ops {
		if op.Seq < 1 || seenSeq[op.Seq] {
			return domain.Invalid("seq", "must be unique and positive")
		}
		seenSeq[op.Seq] = true
		if op.Kind != KindAttendanceCheckIn && op.Kind != KindPaymentEntry {
			return domain.Invalid("kind", "is not a re-playable operation")
		}
		if err := domain.Required("idempotency_key", op.IdempotencyKey); err != nil {
			return err
		}
		if seenKey[op.IdempotencyKey] {
			return domain.Invalid("idempotency_key", "is repeated inside the batch")
		}
		seenKey[op.IdempotencyKey] = true
		if op.ClientCreatedAt.IsZero() {
			return domain.Invalid("client_created_at", "is required")
		}
		if len(op.Payload) == 0 {
			return domain.Invalid("payload", "is required")
		}
	}
	return nil
}

// Submit applies a batch in order. A replayed operation returns the result it
// already has; nothing is applied twice.
func (s *Service) Submit(ctx context.Context, tx pgx.Tx, in SubmitInput) (Result, error) {
	if err := domain.Required("client_id", in.ClientID); err != nil {
		return Result{}, err
	}
	if in.ClientTime.IsZero() {
		return Result{}, domain.Invalid("client_time", "is required")
	}
	if err := ValidateOperations(in.Operations); err != nil {
		return Result{}, err
	}

	serverTime := s.Now().UTC()
	skew := SkewMS(in.ClientTime, serverTime)

	var batchID string
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.sync_batch
		   (legal_entity_id, branch_id, group_id, client_id, client_time,
		    server_time, skew_ms, operation_count)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2, $3, $4, $5, $6
		 FROM pamoja.groups g WHERE g.id = $1::uuid
		 RETURNING id::text`,
		in.GroupID, in.ClientID, in.ClientTime, serverTime, skew, len(in.Operations)).Scan(&batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, domain.ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}

	result := Result{BatchID: batchID, ServerTime: serverTime, SkewMS: skew}
	for _, op := range in.Operations {
		outcome, err := s.applyOne(ctx, tx, in, batchID, op)
		if err != nil {
			return Result{}, err
		}
		result.Operations = append(result.Operations, outcome)
		if outcome.Status == "applied" {
			result.Applied++
		}
		if outcome.Status == "conflict" || outcome.Status == "rejected" {
			result.Conflicts++
		}
	}
	result.Status = BatchStatus(result.Applied, result.Conflicts)

	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.sync_batch
		 SET applied_count = $2, conflict_count = $3, status = $4
		 WHERE id = $1::uuid`, batchID, result.Applied, result.Conflicts, result.Status); err != nil {
		return Result{}, err
	}
	return result, nil
}

// applyOne replays one operation, or returns its recorded outcome when the
// idempotency key has been seen before.
func (s *Service) applyOne(ctx context.Context, tx pgx.Tx, in SubmitInput, batchID string, op OperationInput) (OperationResult, error) {
	var existingStatus, existingReason string
	var existingResult []byte
	err := tx.QueryRow(ctx,
		`SELECT status, conflict_reason, COALESCE(result::text, '')
		 FROM pamoja.sync_operation WHERE idempotency_key = $1`, op.IdempotencyKey).
		Scan(&existingStatus, &existingReason, &existingResult)
	if err == nil {
		out := OperationResult{Seq: op.Seq, Kind: op.Kind, Status: existingStatus,
			ConflictReason: existingReason}
		if existingResult != nil {
			out.Result = json.RawMessage(existingResult)
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperationResult{}, err
	}

	outcome := OperationResult{Seq: op.Seq, Kind: op.Kind}
	// One operation that the server refuses must not poison the whole batch:
	// a savepoint contains any failed statement so the next operation still
	// runs and the refusal is recorded as that operation's outcome.
	if _, err := tx.Exec(ctx, "SAVEPOINT offline_operation"); err != nil {
		return OperationResult{}, err
	}
	var payload []byte
	var applyErr error
	switch op.Kind {
	case KindPaymentEntry:
		payload, outcome, applyErr = s.applyPayment(ctx, tx, in, op)
	case KindAttendanceCheckIn:
		payload, outcome, applyErr = s.applyCheckIn(ctx, tx, in, op)
	}
	if applyErr != nil {
		if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT offline_operation"); rbErr != nil {
			return OperationResult{}, rbErr
		}
		payload = nil
		outcome = OperationResult{Seq: op.Seq, Kind: op.Kind, Status: "rejected",
			ConflictReason: fmt.Sprintf("refused: %v", applyErr)}
	} else if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT offline_operation"); err != nil {
		return OperationResult{}, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO pamoja.sync_operation
		   (batch_id, seq, kind, idempotency_key, client_created_at, payload,
		    status, conflict_reason, result, applied_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6::jsonb, $7, $8, NULLIF($9, '')::jsonb,
		         CASE WHEN $7 = 'applied' THEN now() ELSE NULL END)`,
		batchID, op.Seq, op.Kind, op.IdempotencyKey, op.ClientCreatedAt, string(op.Payload),
		outcome.Status, outcome.ConflictReason, string(payload)); err != nil {
		return OperationResult{}, err
	}
	if len(payload) > 0 && string(payload) != "null" {
		outcome.Result = json.RawMessage(payload)
	}
	return outcome, nil
}

type paymentPayload struct {
	MemberID   string `json:"member_id"`
	MeetingID  string `json:"meeting_id"`
	Category   string `json:"category"`
	AmountUGX  int64  `json:"amount_ugx"`
	PaidOn     string `json:"paid_on"`
	Channel    string `json:"channel"`
	SourceRef  string `json:"source_ref"`
	EvidenceID string `json:"evidence_id"`
}

func (s *Service) applyPayment(ctx context.Context, tx pgx.Tx, in SubmitInput, op OperationInput) ([]byte, OperationResult, error) {
	outcome := OperationResult{Seq: op.Seq, Kind: op.Kind}
	var p paymentPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		outcome.Status, outcome.ConflictReason = "rejected", "the payload is not valid JSON"
		return nil, outcome, nil //nolint:nilerr // a malformed payload is this operation's outcome, not a batch failure
	}
	paidOn := in.ClientTime
	if p.PaidOn != "" {
		parsed, err := time.Parse("2006-01-02", p.PaidOn)
		if err != nil {
			outcome.Status, outcome.ConflictReason = "rejected", "paid_on must be YYYY-MM-DD"
			return nil, outcome, nil //nolint:nilerr // a bad date is this operation's outcome, not a batch failure
		}
		paidOn = parsed
	}
	entry, err := s.Payments.CreateEntry(ctx, tx, payments.CreateEntryInput{
		GroupID: in.GroupID, MemberID: p.MemberID, MeetingID: p.MeetingID,
		Category: p.Category, AmountUGX: p.AmountUGX, PaidOn: paidOn,
		Channel: p.Channel, SourceRef: p.SourceRef, EvidenceID: p.EvidenceID,
		RecordedBy: in.ActorID, RecordedVia: "offline", IdempotencyKey: op.IdempotencyKey,
	})
	if err != nil {
		return classify(outcome, err)
	}
	encoded, _ := json.Marshal(map[string]any{
		"entry_id": entry.ID, "status": entry.Status, "amount_ugx": entry.AmountUGX,
	})
	outcome.Status = "applied"
	return encoded, outcome, nil
}

type checkInPayload struct {
	MeetingID string `json:"meeting_id"`
	MemberID  string `json:"member_id"`
	Status    string `json:"status"`
	Channel   string `json:"channel"`
}

func (s *Service) applyCheckIn(ctx context.Context, tx pgx.Tx, in SubmitInput, op OperationInput) ([]byte, OperationResult, error) {
	outcome := OperationResult{Seq: op.Seq, Kind: op.Kind}
	var p checkInPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		outcome.Status, outcome.ConflictReason = "rejected", "the payload is not valid JSON"
		return nil, outcome, nil //nolint:nilerr // a malformed payload is this operation's outcome, not a batch failure
	}
	if p.Status == "" {
		p.Status = "present"
	}
	if p.Channel == "" {
		p.Channel = "assisted"
	}
	if p.MeetingID == "" || p.MemberID == "" {
		outcome.Status, outcome.ConflictReason = "rejected", "meeting_id and member_id are required"
		return nil, outcome, nil
	}

	var meetingStatus string
	err := tx.QueryRow(ctx,
		`SELECT status FROM pamoja.meeting WHERE id = $1::uuid AND group_id = $2::uuid`,
		p.MeetingID, in.GroupID).Scan(&meetingStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		outcome.Status, outcome.ConflictReason = "rejected", "the meeting is not in this group"
		return nil, outcome, nil
	}
	if err != nil {
		return classify(outcome, err)
	}
	if meetingStatus != "open" {
		// The register moved on while the device was offline; the server wins.
		outcome.Status, outcome.ConflictReason = "conflict", "the register is not open"
		return nil, outcome, nil
	}

	var memberOK int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.membership
		 WHERE member_id = $1::uuid AND group_id = $2::uuid AND status = 'active'`,
		p.MemberID, in.GroupID).Scan(&memberOK); err != nil {
		return classify(outcome, err)
	}
	if memberOK == 0 {
		outcome.Status, outcome.ConflictReason = "rejected", "the member is not active in this group"
		return nil, outcome, nil
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO pamoja.meeting_attendance
		   (legal_entity_id, branch_id, group_id, meeting_id, member_id, status,
		    checkin_channel, recorded_by)
		 SELECT m.legal_entity_id, m.branch_id, m.group_id, m.id, $2::uuid, $3, $4, $5::uuid
		 FROM pamoja.meeting m WHERE m.id = $1::uuid
		 ON CONFLICT (meeting_id, member_id) WHERE amendment_of_id IS NULL DO NOTHING`,
		p.MeetingID, p.MemberID, p.Status, p.Channel, in.ActorID)
	if err != nil {
		return classify(outcome, err)
	}
	if tag.RowsAffected() == 0 {
		outcome.Status, outcome.ConflictReason = "conflict", "the member is already checked in"
		return nil, outcome, nil
	}
	encoded, _ := json.Marshal(map[string]any{
		"meeting_id": p.MeetingID, "member_id": p.MemberID, "status": p.Status,
	})
	outcome.Status = "applied"
	return encoded, outcome, nil
}

// classify turns a service refusal into an operation outcome instead of
// failing the whole batch. Anything that is not a domain refusal is returned
// to the caller, which records it against this operation alone.
func classify(outcome OperationResult, err error) ([]byte, OperationResult, error) {
	switch {
	case errors.Is(err, domain.ErrConflict):
		outcome.Status, outcome.ConflictReason = "conflict", "the record already exists"
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrSeparationOfDuties):
		outcome.Status, outcome.ConflictReason = "rejected", "not permitted"
	case domain.IsValidation(err):
		outcome.Status, outcome.ConflictReason = "rejected", err.Error()
	default:
		return nil, OperationResult{}, err
	}
	return nil, outcome, nil
}
