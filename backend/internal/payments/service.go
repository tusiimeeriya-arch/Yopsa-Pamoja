package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/ledger"
)

// Service applies the payment rules inside one database transaction.
type Service struct {
	Ledger ledger.Adapter
	Now    func() time.Time
}

// New returns a payments service. The sandbox ledger is the default.
func New(adapter ledger.Adapter) *Service {
	if adapter == nil {
		adapter = ledger.NewSandbox()
	}
	return &Service{Ledger: adapter, Now: time.Now}
}

// Entry is a payment entry.
type Entry struct {
	ID           string
	GroupID      string
	MemberID     string
	SubmissionID string
	Category     string
	AmountUGX    int64
	PaidOn       time.Time
	Channel      string
	SourceRef    string
	EvidenceID   string
	Status       string
	RecordedBy   string
	RecordedVia  string
}

// Submission is a locked group submission.
type Submission struct {
	ID            string
	GroupID       string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	DeclaredTotal int64
	EntryCount    int
	Status        string
	LockedBy      string
	SubmittedBy   string
	VerifiedBy    string
	ReceivedTotal int64
	Difference    int64
}

// CreateEntryInput records one member payment.
type CreateEntryInput struct {
	GroupID        string
	MemberID       string
	MeetingID      string
	Category       string
	AmountUGX      int64
	PaidOn         time.Time
	Channel        string
	SourceRef      string
	EvidenceID     string
	RecordedBy     string
	RecordedVia    string
	IdempotencyKey string
}

// CreateEntry validates and inserts a draft entry.
func (s *Service) CreateEntry(ctx context.Context, tx pgx.Tx, in CreateEntryInput) (Entry, error) {
	if err := domain.Required("member_id", in.MemberID); err != nil {
		return Entry{}, err
	}
	if err := domain.PositiveMoney(in.AmountUGX); err != nil {
		return Entry{}, err
	}
	switch in.Category {
	case "savings", "social_fund", "loan_repayment", "fine", "share_capital", "other":
	default:
		return Entry{}, domain.Invalid("category", "is not a known payment category")
	}
	switch in.Channel {
	case "", "cash", "mobile_money", "bank", "other":
	default:
		return Entry{}, domain.Invalid("channel", "is not a known channel")
	}
	if in.Channel == "" {
		in.Channel = "cash"
	}
	if in.PaidOn.IsZero() {
		in.PaidOn = s.Now().UTC()
	}
	if in.PaidOn.After(s.Now().UTC().Add(24 * time.Hour)) {
		return Entry{}, domain.Invalid("paid_on", "cannot be in the future; server time wins")
	}
	if err := domain.Required("idempotency_key", in.IdempotencyKey); err != nil {
		return Entry{}, err
	}
	if in.RecordedVia == "" {
		in.RecordedVia = "self"
	}
	status := "draft"
	if in.RecordedVia == "offline" {
		status = "pending_sync"
	}

	var out Entry
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.payment_entry
		   (legal_entity_id, branch_id, group_id, member_id, meeting_id, category,
		    amount_ugx, paid_on, channel, source_ref, evidence_id, status, recorded_by,
		    recorded_via, idempotency_key)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2::uuid, NULLIF($3, '')::uuid, $4,
		        $5, $6, $7, $8, NULLIF($9, '')::uuid, $10, $11::uuid, $12, $13
		 FROM pamoja.groups g
		 WHERE g.id = $1::uuid
		 RETURNING id, group_id, member_id, COALESCE(submission_id::text, ''), category,
		           amount_ugx, paid_on, channel, source_ref, COALESCE(evidence_id::text, ''),
		           status, recorded_by, recorded_via`,
		in.GroupID, in.MemberID, in.MeetingID, in.Category, in.AmountUGX, in.PaidOn,
		in.Channel, in.SourceRef, in.EvidenceID, status, in.RecordedBy, in.RecordedVia,
		in.IdempotencyKey).
		Scan(&out.ID, &out.GroupID, &out.MemberID, &out.SubmissionID, &out.Category,
			&out.AmountUGX, &out.PaidOn, &out.Channel, &out.SourceRef, &out.EvidenceID,
			&out.Status, &out.RecordedBy, &out.RecordedVia)
	if err != nil {
		if isUniqueViolation(err) {
			return Entry{}, domain.ErrConflict
		}
		return Entry{}, err
	}
	return out, nil
}

// LockInput locks a group's entries for a period.
type LockInput struct {
	GroupID     string
	PeriodStart time.Time
	PeriodEnd   time.Time
	ActorID     string
}

// LockSubmission locks the entries and pins the declared total and hash.
func (s *Service) LockSubmission(ctx context.Context, tx pgx.Tx, in LockInput) (Submission, error) {
	if in.PeriodEnd.Before(in.PeriodStart) {
		return Submission{}, domain.Invalid("period_end", "must not be before period_start")
	}

	var submissionID string
	err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.group_submission
		   (legal_entity_id, branch_id, group_id, period_start, period_end)
		 SELECT legal_entity_id, branch_id, id, $2, $3 FROM pamoja.groups WHERE id = $1::uuid
		 ON CONFLICT (group_id, period_start, period_end) DO NOTHING
		 RETURNING id::text`,
		in.GroupID, in.PeriodStart, in.PeriodEnd).Scan(&submissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM pamoja.group_submission
			 WHERE group_id = $1::uuid AND period_start = $2 AND period_end = $3
			 FOR UPDATE`, in.GroupID, in.PeriodStart, in.PeriodEnd).Scan(&submissionID); err != nil {
			return Submission{}, err
		}
	} else if err != nil {
		return Submission{}, err
	}

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM pamoja.group_submission WHERE id = $1::uuid FOR UPDATE`,
		submissionID).Scan(&status); err != nil {
		return Submission{}, err
	}
	if status != "draft" && status != "returned" {
		return Submission{}, domain.ErrConflict
	}
	if status == "returned" {
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.group_submission SET status = 'draft', version = version + 1 WHERE id = $1::uuid`,
			submissionID); err != nil {
			return Submission{}, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.payment_entry SET status = 'draft', version = version + 1
			 WHERE submission_id = $1::uuid AND status = 'returned'`, submissionID); err != nil {
			return Submission{}, err
		}
	}

	rows, err := tx.Query(ctx,
		`UPDATE pamoja.payment_entry
		 SET submission_id = $1::uuid, updated_at = now(), version = version + 1
		 WHERE group_id = $2::uuid AND (submission_id IS NULL OR submission_id = $1::uuid)
		   AND status IN ('draft', 'pending_sync')
		   AND paid_on >= $3 AND paid_on <= $4
		 RETURNING id::text, amount_ugx, member_id::text`,
		submissionID, in.GroupID, in.PeriodStart, in.PeriodEnd)
	if err != nil {
		return Submission{}, err
	}
	var refs []EntryRef
	var total int64
	for rows.Next() {
		var ref EntryRef
		if err := rows.Scan(&ref.ID, &ref.AmountUGX, &ref.MemberID); err != nil {
			rows.Close()
			return Submission{}, err
		}
		refs = append(refs, ref)
		total += ref.AmountUGX
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Submission{}, err
	}
	if len(refs) == 0 {
		return Submission{}, domain.Invalid("period", "contains no draft payment entries to lock")
	}

	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.group_submission
		 SET status = 'locked', declared_total_ugx = $2, entry_count = $3, locked_hash = $4,
		     locked_by = $5::uuid, locked_at = now(), version = version + 1
		 WHERE id = $1::uuid`,
		submissionID, total, len(refs), LockHash(refs), in.ActorID); err != nil {
		return Submission{}, err
	}
	return s.loadSubmission(ctx, tx, submissionID)
}

// Submit moves a locked submission into review.
func (s *Service) Submit(ctx context.Context, tx pgx.Tx, submissionID, actorID string) (Submission, error) {
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.group_submission
		 SET status = 'under_review', submitted_by = $2::uuid, submitted_at = now(), version = version + 1
		 WHERE id = $1::uuid AND status = 'locked'`, submissionID, actorID); err != nil {
		return Submission{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.payment_entry SET status = 'submitted', version = version + 1
		 WHERE submission_id = $1::uuid AND status IN ('draft', 'pending_sync')`, submissionID); err != nil {
		return Submission{}, err
	}
	return s.loadSubmission(ctx, tx, submissionID)
}

// VerifyInput is one verification decision.
type VerifyInput struct {
	SubmissionID   string
	ReceivedUGX    int64
	Source         string
	ProviderRef    string
	EvidenceID     string
	CashHandoverBy string
	CashDepositBy  string
	Allocations    []Allocation
	Partial        bool
	Note           string
	ActorID        string
}

// VerifyResult reports the outcome, including a posting-pending state.
type VerifyResult struct {
	Submission     Submission
	ReceiptID      string
	Difference     int64
	Partial        bool
	PostingPending bool
	Exceptions     int
}

// Verify records the receipt, allocations and postings. It serialises on the
// submission row, so two concurrent verifications cannot both succeed.
func (s *Service) Verify(ctx context.Context, tx pgx.Tx, in VerifyInput) (VerifyResult, error) {
	var sub Submission
	var lockedBy, submittedBy *string
	err := tx.QueryRow(ctx,
		`SELECT id::text, group_id::text, period_start, period_end, declared_total_ugx,
		        entry_count, status, COALESCE(locked_by::text, ''), COALESCE(submitted_by::text, '')
		 FROM pamoja.group_submission WHERE id = $1::uuid FOR UPDATE`,
		in.SubmissionID).
		Scan(&sub.ID, &sub.GroupID, &sub.PeriodStart, &sub.PeriodEnd, &sub.DeclaredTotal,
			&sub.EntryCount, &sub.Status, &lockedBy, &submittedBy)
	if err != nil {
		return VerifyResult{}, domain.ErrNotFound
	}

	// A duplicate provider callback is a no-op even after the submission has
	// been posted: the unique receipt reference wins.
	var existingReceipt string
	err = tx.QueryRow(ctx,
		`SELECT id::text FROM pamoja.receipt WHERE source = $1 AND provider_ref = $2`,
		in.Source, in.ProviderRef).Scan(&existingReceipt)
	if err == nil {
		return VerifyResult{Submission: sub, ReceiptID: existingReceipt}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return VerifyResult{}, err
	}
	if sub.Status != "under_review" {
		return VerifyResult{}, domain.ErrConflict
	}

	type row struct {
		EntryEvidence
		SourceRef  string
		EvidenceID string
		Recorder   string
	}
	rows, err := tx.Query(ctx,
		`SELECT id::text, amount_ugx, source_ref, COALESCE(evidence_id::text, ''), recorded_by::text
		 FROM pamoja.payment_entry WHERE submission_id = $1::uuid ORDER BY id`, in.SubmissionID)
	if err != nil {
		return VerifyResult{}, err
	}
	var entries []EntryEvidence
	var involved []string
	if lockedBy != nil {
		involved = append(involved, *lockedBy)
	}
	if submittedBy != nil {
		involved = append(involved, *submittedBy)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.EntryID, &r.AmountUGX, &r.SourceRef, &r.EvidenceID, &r.Recorder); err != nil {
			rows.Close()
			return VerifyResult{}, err
		}
		r.Evidenced = r.SourceRef != "" && r.EvidenceID != ""
		entries = append(entries, r.EntryEvidence)
		involved = append(involved, r.Recorder)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return VerifyResult{}, err
	}

	if err := CheckVerifier(in.ActorID, involved); err != nil {
		return VerifyResult{}, domain.ErrSeparationOfDuties
	}
	if err := RequireEvidence(in.Source, in.EvidenceID); err != nil {
		return VerifyResult{}, domain.Invalid("evidence_id", "a source reference and evidence record are required")
	}
	if in.Source == "cash" {
		if err := CheckCashPath(in.ActorID, in.CashHandoverBy, in.CashDepositBy); err != nil {
			return VerifyResult{}, domain.Invalid("cash_path", "receipt, handover and deposit need different people")
		}
	}

	plan, err := PlanVerification(sub.DeclaredTotal, in.ReceivedUGX, entries, in.Allocations, in.Partial)
	if err != nil {
		switch {
		case errors.Is(err, ErrSelfVerification):
			return VerifyResult{}, domain.ErrSeparationOfDuties
		case errors.Is(err, ErrEntryNotEvidenced), errors.Is(err, ErrOverAllocated),
			errors.Is(err, ErrEmptyPlan), errors.Is(err, ErrDuplicateAllocation):
			return VerifyResult{}, domain.Invalid("allocation", err.Error())
		default:
			return VerifyResult{}, domain.ErrConflict
		}
	}

	var receiptID string
	err = tx.QueryRow(ctx,
		`INSERT INTO pamoja.receipt
		   (legal_entity_id, branch_id, group_id, submission_id, amount_ugx, source, provider_ref,
		    evidence_id, status, recorded_by, confirmed_by, received_on, cash_handover_by, cash_deposit_by)
		 SELECT g.legal_entity_id, g.branch_id, g.id, $2::uuid, $3, $4, $5, NULLIF($6, '')::uuid,
		        'confirmed', $7::uuid, $7::uuid, current_date, NULLIF($8, '')::uuid, NULLIF($9, '')::uuid
		 FROM pamoja.groups g WHERE g.id = $1::uuid
		 RETURNING id::text`,
		sub.GroupID, sub.ID, in.ReceivedUGX, in.Source, in.ProviderRef, in.EvidenceID,
		in.ActorID, in.CashHandoverBy, in.CashDepositBy).Scan(&receiptID)
	if err != nil {
		return VerifyResult{}, err
	}

	// Record the verification state before allocations/postings.
	nextStatus := "verified"
	if plan.Partial {
		nextStatus = "partially_verified"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.group_submission
		 SET status = $2, verified_by = $3::uuid, verified_at = now(), received_total_ugx = $4,
		     decision_note = $5, version = version + 1
		 WHERE id = $1::uuid`,
		sub.ID, nextStatus, in.ActorID, in.ReceivedUGX, in.Note); err != nil {
		return VerifyResult{}, err
	}

	result := VerifyResult{ReceiptID: receiptID, Difference: plan.Difference, Partial: plan.Partial}
	for _, alloc := range plan.Allocations {
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.payment_entry SET status = 'verified', version = version + 1
			 WHERE id = $1::uuid AND status = 'submitted'`, alloc.EntryID); err != nil {
			return VerifyResult{}, err
		}
		var allocationID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.allocation
			   (legal_entity_id, branch_id, group_id, receipt_id, entry_id, amount_ugx, created_by)
			 SELECT g.legal_entity_id, g.branch_id, g.id, $2::uuid, $3::uuid, $4, $5::uuid
			 FROM pamoja.groups g WHERE g.id = $1::uuid
			 RETURNING id::text`,
			sub.GroupID, receiptID, alloc.EntryID, alloc.AmountUGX, in.ActorID).Scan(&allocationID); err != nil {
			return VerifyResult{}, err
		}

		externalRef := "SBX-" + alloc.EntryID
		postedRef, postErr := s.Ledger.PostRepayment(ctx, tx, ledger.Posting{
			ExternalRef: externalRef, GroupID: sub.GroupID, AmountUGX: alloc.AmountUGX, Kind: "repayment",
		})
		if postErr != nil {
			if _, err := tx.Exec(ctx,
				`INSERT INTO pamoja.ledger_posting
				   (legal_entity_id, branch_id, group_id, allocation_id, external_ref, status, attempts, last_error)
				 SELECT g.legal_entity_id, g.branch_id, g.id, $2::uuid, $3, 'failed', 1, $4
				 FROM pamoja.groups g WHERE g.id = $1::uuid`,
				sub.GroupID, allocationID, externalRef, postErr.Error()); err != nil {
				return VerifyResult{}, err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE pamoja.allocation SET status = 'failed' WHERE id = $1::uuid`, allocationID); err != nil {
				return VerifyResult{}, err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE pamoja.payment_entry SET status = 'finance_exception', version = version + 1
				 WHERE id = $1::uuid AND status IN ('submitted', 'verified')`, alloc.EntryID); err != nil {
				return VerifyResult{}, err
			}
			result.PostingPending = true
			result.Exceptions++
			continue
		}

		var postingID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.ledger_posting
			   (legal_entity_id, branch_id, group_id, allocation_id, external_ref, status, posted_at, confirmed_by, confirmed_at)
			 SELECT g.legal_entity_id, g.branch_id, g.id, $2::uuid, $3, 'confirmed', now(), $4::uuid, now()
			 FROM pamoja.groups g WHERE g.id = $1::uuid
			 RETURNING id::text`,
			sub.GroupID, allocationID, postedRef, in.ActorID).Scan(&postingID); err != nil {
			return VerifyResult{}, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.allocation SET status = 'posted' WHERE id = $1::uuid`, allocationID); err != nil {
			return VerifyResult{}, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.payment_entry SET status = 'posted', version = version + 1
			 WHERE id = $1::uuid`, alloc.EntryID); err != nil {
			return VerifyResult{}, err
		}
	}

	if result.PostingPending {
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.group_submission SET status = 'posting_pending', version = version + 1
			 WHERE id = $1::uuid`, sub.ID); err != nil {
			return VerifyResult{}, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.finance_exception
			   (legal_entity_id, branch_id, group_id, kind, submission_id, receipt_id, owner_user_id, due_on)
			 SELECT g.legal_entity_id, g.branch_id, g.id, 'posting_failed', $2::uuid, $3::uuid, $4::uuid,
			        current_date + 7
			 FROM pamoja.groups g WHERE g.id = $1::uuid`,
			sub.GroupID, sub.ID, receiptID, in.ActorID); err != nil {
			return VerifyResult{}, err
		}
	} else {
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.group_submission SET status = 'posted', version = version + 1
			 WHERE id = $1::uuid`, sub.ID); err != nil {
			return VerifyResult{}, err
		}
		if plan.Partial && plan.Total < in.ReceivedUGX {
			if _, err := tx.Exec(ctx,
				`INSERT INTO pamoja.finance_exception
				   (legal_entity_id, branch_id, group_id, kind, submission_id, receipt_id, owner_user_id, due_on)
				 SELECT g.legal_entity_id, g.branch_id, g.id, 'unallocated_receipt', $2::uuid, $3::uuid,
				        $4::uuid, current_date + 7
				 FROM pamoja.groups g WHERE g.id = $1::uuid`,
				sub.GroupID, sub.ID, receiptID, in.ActorID); err != nil {
				return VerifyResult{}, err
			}
			result.Exceptions++
		}
	}

	updated, err := s.loadSubmission(ctx, tx, sub.ID)
	if err != nil {
		return VerifyResult{}, err
	}
	result.Submission = updated
	return result, nil
}

// Return sends a submission back with a reason; entries become returned.
func (s *Service) Return(ctx context.Context, tx pgx.Tx, submissionID, actorID, reason string) (Submission, error) {
	if reason == "" {
		return Submission{}, domain.Invalid("reason", "is required")
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.group_submission
		 SET status = 'returned', decision_note = $2, version = version + 1
		 WHERE id = $1::uuid AND status = 'under_review'`, submissionID, reason); err != nil {
		return Submission{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.payment_entry SET status = 'returned', version = version + 1
		 WHERE submission_id = $1::uuid AND status = 'submitted'`, submissionID); err != nil {
		return Submission{}, err
	}
	_ = actorID
	return s.loadSubmission(ctx, tx, submissionID)
}

// RetryPosting asks the ledger to confirm a failed posting.
func (s *Service) RetryPosting(ctx context.Context, tx pgx.Tx, postingID, actorID string) (bool, error) {
	var externalRef, status, submissionID, groupID string
	var amount int64
	err := tx.QueryRow(ctx,
		`SELECT p.external_ref, p.status, e.submission_id::text, a.group_id::text, a.amount_ugx
		 FROM pamoja.ledger_posting p
		 JOIN pamoja.allocation a ON a.id = p.allocation_id
		 JOIN pamoja.payment_entry e ON e.id = a.entry_id
		 WHERE p.id = $1::uuid`, postingID).Scan(&externalRef, &status, &submissionID, &groupID, &amount)
	if err != nil {
		return false, domain.ErrNotFound
	}
	if status == "confirmed" {
		return true, nil
	}
	// Retry the original posting, not just the confirmation: the first attempt
	// may have failed before the ledger recorded anything.
	if _, err := s.Ledger.PostRepayment(ctx, tx, ledger.Posting{
		ExternalRef: externalRef, GroupID: groupID, AmountUGX: amount, Kind: "repayment",
	}); err != nil {
		if _, execErr := tx.Exec(ctx,
			`UPDATE pamoja.ledger_posting SET attempts = attempts + 1, last_error = $2 WHERE id = $1::uuid`,
			postingID, err.Error()); execErr != nil {
			return false, execErr
		}
		return false, nil
	}
	ok, err := s.Ledger.ConfirmPosting(ctx, tx, externalRef)
	if err != nil || !ok {
		if _, execErr := tx.Exec(ctx,
			`UPDATE pamoja.ledger_posting SET attempts = attempts + 1, last_error = $2 WHERE id = $1::uuid`,
			postingID, fmt.Sprint(err)); execErr != nil {
			return false, execErr
		}
		return false, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.ledger_posting SET status = 'confirmed', posted_at = now(),
		 confirmed_by = $2::uuid, confirmed_at = now(), last_error = '' WHERE id = $1::uuid`,
		postingID, actorID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.allocation SET status = 'posted'
		 WHERE id = (SELECT allocation_id FROM pamoja.ledger_posting WHERE id = $1::uuid)`, postingID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pamoja.payment_entry SET status = 'posted', version = version + 1
		 WHERE id = (SELECT a.entry_id FROM pamoja.allocation a
		             JOIN pamoja.ledger_posting p ON p.allocation_id = a.id WHERE p.id = $1::uuid)`, postingID); err != nil {
		return false, err
	}
	var remaining int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.ledger_posting p
		 JOIN pamoja.allocation a ON a.id = p.allocation_id
		 JOIN pamoja.payment_entry e ON e.id = a.entry_id
		 WHERE e.submission_id = $1::uuid AND p.status <> 'confirmed'`, submissionID).Scan(&remaining); err != nil {
		return false, err
	}
	if remaining == 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.group_submission SET status = 'posted', version = version + 1
			 WHERE id = $1::uuid AND status IN ('posting_pending', 'finance_exception')`, submissionID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Summary distinguishes submitted, verified and posted money.
type Summary struct {
	SubmittedUGX int64 `json:"submitted_ugx"`
	VerifiedUGX  int64 `json:"verified_ugx"`
	PostedUGX    int64 `json:"posted_ugx"`
	PendingUGX   int64 `json:"posting_pending_ugx"`
	Exceptions   int   `json:"open_exceptions"`
}

// Summarise reads the three values separately; pending entries never change an
// authoritative balance.
func (s *Service) Summarise(ctx context.Context, tx pgx.Tx, groupID string) (Summary, error) {
	var out Summary
	if err := tx.QueryRow(ctx,
		`SELECT
		   COALESCE(sum(amount_ugx) FILTER (WHERE status = 'submitted'), 0),
		   COALESCE(sum(amount_ugx) FILTER (WHERE status IN ('verified', 'posting_pending', 'finance_exception')), 0),
		   COALESCE(sum(amount_ugx) FILTER (WHERE status = 'posted'), 0)
		 FROM pamoja.payment_entry WHERE group_id = $1::uuid`, groupID).
		Scan(&out.SubmittedUGX, &out.VerifiedUGX, &out.PostedUGX); err != nil {
		return Summary{}, err
	}
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(sum(a.amount_ugx), 0)
		 FROM pamoja.allocation a JOIN pamoja.ledger_posting p ON p.allocation_id = a.id
		 WHERE a.group_id = $1::uuid AND p.status = 'failed'`, groupID).Scan(&out.PendingUGX); err != nil {
		return Summary{}, err
	}
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM pamoja.finance_exception
		 WHERE group_id = $1::uuid AND status <> 'resolved'`, groupID).Scan(&out.Exceptions); err != nil {
		return Summary{}, err
	}
	return out, nil
}

func (s *Service) loadSubmission(ctx context.Context, tx pgx.Tx, id string) (Submission, error) {
	var out Submission
	err := tx.QueryRow(ctx,
		`SELECT id::text, group_id::text, period_start, period_end, declared_total_ugx,
		        entry_count, status, COALESCE(locked_by::text, ''), COALESCE(submitted_by::text, ''),
		        COALESCE(verified_by::text, ''), COALESCE(received_total_ugx, 0)
		 FROM pamoja.group_submission WHERE id = $1::uuid`, id).
		Scan(&out.ID, &out.GroupID, &out.PeriodStart, &out.PeriodEnd, &out.DeclaredTotal,
			&out.EntryCount, &out.Status, &out.LockedBy, &out.SubmittedBy, &out.VerifiedBy, &out.ReceivedTotal)
	if err != nil {
		return Submission{}, err
	}
	out.Difference = out.DeclaredTotal - out.ReceivedTotal
	return out, nil
}

func isUniqueViolation(err error) bool {
	type sqlState interface{ SQLState() string }
	var s sqlState
	if errors.As(err, &s) {
		return s.SQLState() == "23505"
	}
	return false
}

// NewEntryID is a server-side UUIDv7 for callers that need an id before insert.
func NewEntryID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
