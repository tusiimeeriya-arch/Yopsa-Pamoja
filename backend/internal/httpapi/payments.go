package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/payments"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

type entryResponse struct {
	ID           string `json:"id"`
	GroupID      string `json:"group_id"`
	MemberID     string `json:"member_id"`
	SubmissionID string `json:"submission_id,omitempty"`
	Category     string `json:"category"`
	AmountUGX    int64  `json:"amount_ugx"`
	PaidOn       string `json:"paid_on"`
	Channel      string `json:"channel"`
	SourceRef    string `json:"source_ref,omitempty"`
	EvidenceID   string `json:"evidence_id,omitempty"`
	Status       string `json:"status"`
	RecordedBy   string `json:"recorded_by"`
	RecordedVia  string `json:"recorded_via"`
}

func entryJSON(e payments.Entry) entryResponse {
	return entryResponse{
		ID: e.ID, GroupID: e.GroupID, MemberID: e.MemberID, SubmissionID: e.SubmissionID,
		Category: e.Category, AmountUGX: e.AmountUGX, PaidOn: e.PaidOn.Format("2006-01-02"),
		Channel: e.Channel, SourceRef: e.SourceRef, EvidenceID: e.EvidenceID,
		Status: e.Status, RecordedBy: e.RecordedBy, RecordedVia: e.RecordedVia,
	}
}

type submissionResponse struct {
	ID            string `json:"id"`
	GroupID       string `json:"group_id"`
	PeriodStart   string `json:"period_start"`
	PeriodEnd     string `json:"period_end"`
	DeclaredTotal int64  `json:"declared_total_ugx"`
	EntryCount    int    `json:"entry_count"`
	Status        string `json:"status"`
	ReceivedTotal int64  `json:"received_total_ugx"`
	Difference    int64  `json:"difference_ugx"`
}

func submissionJSON(s payments.Submission) submissionResponse {
	return submissionResponse{
		ID: s.ID, GroupID: s.GroupID,
		PeriodStart: s.PeriodStart.Format("2006-01-02"), PeriodEnd: s.PeriodEnd.Format("2006-01-02"),
		DeclaredTotal: s.DeclaredTotal, EntryCount: s.EntryCount, Status: s.Status,
		ReceivedTotal: s.ReceivedTotal, Difference: s.Difference,
	}
}

type createEntryRequest struct {
	MemberID       string `json:"member_id"`
	MeetingID      string `json:"meeting_id"`
	Category       string `json:"category"`
	AmountUGX      int64  `json:"amount_ugx"`
	PaidOn         string `json:"paid_on"`
	Channel        string `json:"channel"`
	SourceRef      string `json:"source_ref"`
	EvidenceID     string `json:"evidence_id"`
	RecordedVia    string `json:"recorded_via"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (d Deps) handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req createEntryRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		paidOn := time.Now().UTC()
		if req.PaidOn != "" {
			parsed, err := time.Parse("2006-01-02", req.PaidOn)
			if err != nil {
				return 0, nil, domain.Invalid("paid_on", "must be YYYY-MM-DD")
			}
			paidOn = parsed
		}
		entry, err := d.Payments.CreateEntry(ctx, tx, payments.CreateEntryInput{
			GroupID: groupID, MemberID: req.MemberID, MeetingID: req.MeetingID,
			Category: req.Category, AmountUGX: req.AmountUGX, PaidOn: paidOn,
			Channel: req.Channel, SourceRef: req.SourceRef, EvidenceID: req.EvidenceID,
			RecordedBy: p.UserID.String(), RecordedVia: req.RecordedVia,
			IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.entry.create", EntityType: "payment_entry", EntityID: entry.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"amount_ugx": entry.AmountUGX, "category": entry.Category, "member_id": entry.MemberID},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, entryJSON(entry), nil
	})
}

type updateEntryRequest struct {
	AmountUGX  int64  `json:"amount_ugx"`
	Category   string `json:"category"`
	Channel    string `json:"channel"`
	SourceRef  string `json:"source_ref"`
	EvidenceID string `json:"evidence_id"`
	PaidOn     string `json:"paid_on"`
}

func (d Deps) handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	entryID := idFrom(r, "entryID")
	groupID, err := d.entryGroup(r, entryID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionEditDraft, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req updateEntryRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		if err := domain.PositiveMoney(req.AmountUGX); err != nil {
			return 0, nil, err
		}
		var paidOn time.Time
		if req.PaidOn != "" {
			paidOn, err = time.Parse("2006-01-02", req.PaidOn)
			if err != nil {
				return 0, nil, domain.Invalid("paid_on", "must be YYYY-MM-DD")
			}
		} else {
			paidOn = time.Now().UTC()
		}
		var out entryResponse
		err := tx.QueryRow(ctx,
			`UPDATE pamoja.payment_entry
			 SET amount_ugx = $2, category = $3, channel = $4, source_ref = $5,
			     evidence_id = NULLIF($6, '')::uuid, paid_on = $7, version = version + 1, updated_at = now()
			 WHERE id = $1::uuid AND status = 'draft'
			 RETURNING id, group_id, member_id, COALESCE(submission_id::text, ''), category,
			           amount_ugx, paid_on, channel, source_ref, COALESCE(evidence_id::text, ''),
			           status, recorded_by, recorded_via`,
			entryID, req.AmountUGX, req.Category, req.Channel, req.SourceRef, req.EvidenceID, paidOn).
			Scan(&out.ID, &out.GroupID, &out.MemberID, &out.SubmissionID, &out.Category,
				&out.AmountUGX, &out.PaidOn, &out.Channel, &out.SourceRef, &out.EvidenceID,
				&out.Status, &out.RecordedBy, &out.RecordedVia)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, domain.ErrConflict
		}
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.entry.update", EntityType: "payment_entry", EntityID: entryID,
			ScopeType: "group", ScopeID: groupID, After: map[string]any{"amount_ugx": req.AmountUGX},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, out, nil
	})
}

func (d Deps) handleListEntries(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var out []entryResponse
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, group_id, member_id, COALESCE(submission_id::text, ''), category,
			        amount_ugx, paid_on, channel, source_ref, COALESCE(evidence_id::text, ''),
			        status, recorded_by, recorded_via
			 FROM pamoja.payment_entry WHERE group_id = $1::uuid
			 ORDER BY paid_on DESC, created_at DESC LIMIT 500`, groupID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e entryResponse
			if err := rows.Scan(&e.ID, &e.GroupID, &e.MemberID, &e.SubmissionID, &e.Category,
				&e.AmountUGX, &e.PaidOn, &e.Channel, &e.SourceRef, &e.EvidenceID,
				&e.Status, &e.RecordedBy, &e.RecordedVia); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

type lockSubmissionRequest struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

func (d Deps) handleLockSubmission(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionLock, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req lockSubmissionRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		start, err := time.Parse("2006-01-02", req.PeriodStart)
		if err != nil {
			return 0, nil, domain.Invalid("period_start", "must be YYYY-MM-DD")
		}
		end, err := time.Parse("2006-01-02", req.PeriodEnd)
		if err != nil {
			return 0, nil, domain.Invalid("period_end", "must be YYYY-MM-DD")
		}
		sub, err := d.Payments.LockSubmission(ctx, tx, payments.LockInput{
			GroupID: groupID, PeriodStart: start, PeriodEnd: end, ActorID: p.UserID.String(),
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.submission.lock", EntityType: "group_submission", EntityID: sub.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"declared_total_ugx": sub.DeclaredTotal, "entry_count": sub.EntryCount},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, submissionJSON(sub), nil
	})
}

func (d Deps) handleSubmitSubmission(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	submissionID := idFrom(r, "submissionID")
	groupID, err := d.submissionGroup(r, submissionID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionSubmit, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, _ []byte) (int, any, error) {
		sub, err := d.Payments.Submit(ctx, tx, submissionID, p.UserID.String())
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.submission.submit", EntityType: "group_submission", EntityID: submissionID,
			ScopeType: "group", ScopeID: groupID, After: map[string]any{"status": sub.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, submissionJSON(sub), nil
	})
}

type verifySubmissionRequest struct {
	ReceivedUGX    int64  `json:"received_ugx"`
	Source         string `json:"source"`
	ProviderRef    string `json:"provider_ref"`
	EvidenceID     string `json:"evidence_id"`
	CashHandoverBy string `json:"cash_handover_by"`
	CashDepositBy  string `json:"cash_deposit_by"`
	Partial        bool   `json:"partial"`
	Note           string `json:"note"`
	Allocations    []struct {
		EntryID   string `json:"entry_id"`
		AmountUGX int64  `json:"amount_ugx"`
	} `json:"allocations"`
}

func (d Deps) handleVerifySubmission(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if !d.stepUpFresh(p) {
		d.writeDomainError(w, r, domain.ErrStepUpRequired)
		return
	}
	submissionID := idFrom(r, "submissionID")
	groupID, err := d.submissionGroup(r, submissionID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionVerify, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req verifySubmissionRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		var allocations []payments.Allocation
		for _, a := range req.Allocations {
			allocations = append(allocations, payments.Allocation{EntryID: a.EntryID, AmountUGX: a.AmountUGX})
		}
		result, err := d.Payments.Verify(ctx, tx, payments.VerifyInput{
			SubmissionID: submissionID, ReceivedUGX: req.ReceivedUGX, Source: req.Source,
			ProviderRef: req.ProviderRef, EvidenceID: req.EvidenceID,
			CashHandoverBy: req.CashHandoverBy, CashDepositBy: req.CashDepositBy,
			Allocations: allocations, Partial: req.Partial, Note: req.Note,
			ActorID: p.UserID.String(),
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.submission.verify", EntityType: "group_submission", EntityID: submissionID,
			ScopeType: "group", ScopeID: groupID, Reason: req.Note,
			After: map[string]any{
				"received_ugx": req.ReceivedUGX, "difference_ugx": result.Difference,
				"partial": result.Partial, "posting_pending": result.PostingPending,
				"receipt_id": result.ReceiptID,
			},
		}); err != nil {
			return 0, nil, err
		}
		status := http.StatusOK
		if result.PostingPending {
			status = http.StatusAccepted
		}
		return status, map[string]any{
			"submission":      submissionJSON(result.Submission),
			"receipt_id":      result.ReceiptID,
			"difference_ugx":  result.Difference,
			"partial":         result.Partial,
			"posting_pending": result.PostingPending,
			"exceptions":      result.Exceptions,
		}, nil
	})
}

type returnSubmissionRequest struct {
	Reason string `json:"reason"`
}

func (d Deps) handleReturnSubmission(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	submissionID := idFrom(r, "submissionID")
	groupID, err := d.submissionGroup(r, submissionID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionReturn, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req returnSubmissionRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		sub, err := d.Payments.Return(ctx, tx, submissionID, p.UserID.String(), req.Reason)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.submission.return", EntityType: "group_submission", EntityID: submissionID,
			ScopeType: "group", ScopeID: groupID, Reason: req.Reason,
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, submissionJSON(sub), nil
	})
}

func (d Deps) handleRetryPosting(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if !d.stepUpFresh(p) {
		d.writeDomainError(w, r, domain.ErrStepUpRequired)
		return
	}
	postingID := idFrom(r, "postingID")
	groupID, err := d.postingGroup(r, postingID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleReconciliation, rbac.ActionPost, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, _ []byte) (int, any, error) {
		ok, err := d.Payments.RetryPosting(ctx, tx, postingID, p.UserID.String())
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "reconciliation.posting.retry", EntityType: "ledger_posting", EntityID: postingID,
			ScopeType: "group", ScopeID: groupID, After: map[string]any{"confirmed": ok},
		}); err != nil {
			return 0, nil, err
		}
		if !ok {
			return http.StatusAccepted, map[string]any{"status": "posting_pending"}, nil
		}
		return http.StatusOK, map[string]any{"status": "posted"}, nil
	})
}

func (d Deps) handlePaymentsSummary(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var summary payments.Summary
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		summary, err = d.Payments.Summarise(ctx, tx, groupID)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

type correctionRequest struct {
	GroupID    string `json:"group_id"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Kind       string `json:"kind"`
	Reason     string `json:"reason"`
}

func (d Deps) handleCreateCorrection(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if !d.stepUpFresh(p) {
		d.writeDomainError(w, r, domain.ErrStepUpRequired)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req correctionRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		if err := domain.Required("reason", req.Reason); err != nil {
			return 0, nil, err
		}
		if req.EntityType != "payment_entry" && req.EntityType != "receipt" {
			return 0, nil, domain.Invalid("entity_type", "must be payment_entry or receipt")
		}
		if err := d.authorizeGroup(ctx, p, rbac.ModulePayments, rbac.ActionCorrect, req.GroupID, RequestIDFrom(r.Context())); err != nil {
			return 0, nil, err
		}
		var snapshot []byte
		switch req.EntityType {
		case "receipt":
			if err := tx.QueryRow(ctx, `SELECT to_jsonb(r) FROM pamoja.receipt r WHERE r.id = $1::uuid AND r.group_id = $2::uuid`,
				req.EntityID, req.GroupID).Scan(&snapshot); err != nil {
				return 0, nil, domain.ErrNotFound
			}
		case "payment_entry":
			if err := tx.QueryRow(ctx, `SELECT to_jsonb(e) FROM pamoja.payment_entry e WHERE e.id = $1::uuid AND e.group_id = $2::uuid`,
				req.EntityID, req.GroupID).Scan(&snapshot); err != nil {
				return 0, nil, domain.ErrNotFound
			}
		}
		var id string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.correction
			   (legal_entity_id, branch_id, group_id, entity_type, entity_id, kind, reason,
			    requested_by, original_snapshot)
			 SELECT g.legal_entity_id, g.branch_id, g.id, $2, $3::uuid, $4, $5, $6::uuid, $7::jsonb
			 FROM pamoja.groups g WHERE g.id = $1::uuid
			 RETURNING id::text`,
			req.GroupID, req.EntityType, req.EntityID, req.Kind, req.Reason, p.UserID, string(snapshot)).Scan(&id); err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.correction.request", EntityType: "correction", EntityID: id,
			ScopeType: "group", ScopeID: req.GroupID, Reason: req.Reason,
			After: map[string]any{"entity_type": req.EntityType, "entity_id": req.EntityID, "kind": req.Kind},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, map[string]string{"id": id, "status": "requested"}, nil
	})
}

func (d Deps) handleApproveCorrection(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if !d.stepUpFresh(p) {
		d.writeDomainError(w, r, domain.ErrStepUpRequired)
		return
	}
	correctionID := idFrom(r, "correctionID")
	groupID, requestedBy, err := d.correctionGroup(r, correctionID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionCorrect, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.checkSeparation(p, rbac.ModulePayments, rbac.ActionCorrect,
		map[rbac.ContextField][]string{rbac.CtxRequester: {requestedBy}}); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, _ []byte) (int, any, error) {
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.correction SET status = 'approved', approved_by = $2::uuid, approved_at = now()
			 WHERE id = $1::uuid AND status = 'requested'`, correctionID, p.UserID); err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.correction.approve", EntityType: "correction", EntityID: correctionID,
			ScopeType: "group", ScopeID: groupID,
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]string{"status": "approved"}, nil
	})
}

func (d Deps) handleApplyCorrection(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if !d.stepUpFresh(p) {
		d.writeDomainError(w, r, domain.ErrStepUpRequired)
		return
	}
	correctionID := idFrom(r, "correctionID")
	groupID, _, err := d.correctionGroup(r, correctionID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModulePayments, rbac.ActionCorrect, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, _ []byte) (int, any, error) {
		var entityType, entityID string
		if err := tx.QueryRow(ctx,
			`SELECT entity_type, entity_id::text FROM pamoja.correction WHERE id = $1::uuid AND status = 'approved'`,
			correctionID).Scan(&entityType, &entityID); err != nil {
			return 0, nil, domain.ErrConflict
		}
		if entityType == "receipt" {
			if _, err := tx.Exec(ctx,
				`UPDATE pamoja.receipt SET status = 'voided' WHERE id = $1::uuid AND status = 'confirmed'`, entityID); err != nil {
				return 0, nil, err
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.correction SET status = 'applied' WHERE id = $1::uuid`, correctionID); err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "payments.correction.apply", EntityType: "correction", EntityID: correctionID,
			ScopeType: "group", ScopeID: groupID,
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]string{"status": "applied"}, nil
	})
}

type exceptionResponse struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	SubmissionID   string `json:"submission_id,omitempty"`
	ReceiptID      string `json:"receipt_id,omitempty"`
	PostingID      string `json:"posting_id,omitempty"`
	OwnerUserID    string `json:"owner_user_id"`
	DueOn          string `json:"due_on"`
	Status         string `json:"status"`
	ResolutionNote string `json:"resolution_note,omitempty"`
}

func (d Deps) handleListExceptions(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := r.URL.Query().Get("group_id")
	if groupID == "" {
		d.writeDomainError(w, r, domain.Invalid("group_id", "is required"))
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleReconciliation, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var out []exceptionResponse
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, kind, COALESCE(submission_id::text, ''), COALESCE(receipt_id::text, ''),
			        COALESCE(posting_id::text, ''), owner_user_id, due_on, status, resolution_note
			 FROM pamoja.finance_exception WHERE group_id = $1::uuid ORDER BY created_at DESC`, groupID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e exceptionResponse
			var due time.Time
			if err := rows.Scan(&e.ID, &e.Kind, &e.SubmissionID, &e.ReceiptID, &e.PostingID,
				&e.OwnerUserID, &due, &e.Status, &e.ResolutionNote); err != nil {
				return err
			}
			e.DueOn = due.Format("2006-01-02")
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exceptions": out})
}

type resolveExceptionRequest struct {
	Note string `json:"note"`
}

func (d Deps) handleResolveException(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if !d.stepUpFresh(p) {
		d.writeDomainError(w, r, domain.ErrStepUpRequired)
		return
	}
	exceptionID := idFrom(r, "exceptionID")
	groupID, err := d.exceptionGroup(r, exceptionID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleReconciliation, rbac.ActionPost, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req resolveExceptionRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		if req.Note == "" {
			return 0, nil, domain.Invalid("note", "is required")
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pamoja.finance_exception
			 SET status = 'resolved', resolution_note = $2, resolved_by = $3::uuid, resolved_at = now()
			 WHERE id = $1::uuid`, exceptionID, req.Note, p.UserID); err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "reconciliation.exception.resolve", EntityType: "finance_exception", EntityID: exceptionID,
			ScopeType: "group", ScopeID: groupID, Reason: req.Note,
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]string{"status": "resolved"}, nil
	})
}

func (d Deps) entryGroup(r *http.Request, id string) (string, error) {
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT group_id::text FROM pamoja.payment_entry WHERE id = $1::uuid`, id).Scan(&groupID)
	})
	return groupID, err
}

func (d Deps) submissionGroup(r *http.Request, id string) (string, error) {
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT group_id::text FROM pamoja.group_submission WHERE id = $1::uuid`, id).Scan(&groupID)
	})
	return groupID, err
}

func (d Deps) postingGroup(r *http.Request, id string) (string, error) {
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT group_id::text FROM pamoja.ledger_posting WHERE id = $1::uuid`, id).Scan(&groupID)
	})
	return groupID, err
}

func (d Deps) correctionGroup(r *http.Request, id string) (string, string, error) {
	var groupID, requestedBy string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT group_id::text, requested_by::text FROM pamoja.correction WHERE id = $1::uuid`, id).
			Scan(&groupID, &requestedBy)
	})
	return groupID, requestedBy, err
}

func (d Deps) exceptionGroup(r *http.Request, id string) (string, error) {
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT group_id::text FROM pamoja.finance_exception WHERE id = $1::uuid`, id).Scan(&groupID)
	})
	return groupID, err
}
