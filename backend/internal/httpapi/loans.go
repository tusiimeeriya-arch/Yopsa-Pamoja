package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yopsa/pamoja/backend/internal/audit"
	"github.com/yopsa/pamoja/backend/internal/domain"
	"github.com/yopsa/pamoja/backend/internal/loans"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

type loanResponse struct {
	ID                 string `json:"id"`
	GroupID            string `json:"group_id"`
	ProductCode        string `json:"product_code"`
	ProductVersion     int    `json:"product_version"`
	Kind               string `json:"loan_kind"`
	RequestedAmountUGX int64  `json:"requested_amount_ugx"`
	ApprovedAmountUGX  int64  `json:"approved_amount_ugx,omitempty"`
	TermMonths         int    `json:"term_months"`
	Purpose            string `json:"purpose"`
	Status             string `json:"status"`
	AppliedBy          string `json:"applied_by"`
	AppraiserID        string `json:"appraiser_id,omitempty"`
	ApprovedBy         string `json:"approved_by,omitempty"`
	MaterialChanges    int    `json:"material_change_count"`
	CreatedAt          string `json:"created_at"`
}

func loanJSON(l loans.Loan) loanResponse {
	return loanResponse{
		ID: l.ID, GroupID: l.GroupID, ProductCode: l.ProductCode, ProductVersion: l.ProductVersion,
		Kind: l.Kind, RequestedAmountUGX: l.RequestedAmountUGX, ApprovedAmountUGX: l.ApprovedAmountUGX,
		TermMonths: l.TermMonths, Purpose: l.Purpose, Status: l.Status, AppliedBy: l.AppliedBy,
		AppraiserID: l.AppraiserID, ApprovedBy: l.ApprovedBy, MaterialChanges: l.MaterialChanges,
		CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type createLoanRequest struct {
	ProductCode        string `json:"product_code"`
	ProductVersion     int    `json:"product_version"`
	Kind               string `json:"loan_kind"`
	RequestedAmountUGX int64  `json:"requested_amount_ugx"`
	TermMonths         int    `json:"term_months"`
	Purpose            string `json:"purpose"`
	RepaymentSource    string `json:"repayment_source"`
	AuthorityRecordID  string `json:"authority_record_id"`
}

func (d Deps) handleCreateLoanApplication(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req createLoanRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		loan, err := d.Loans.CreateApplication(ctx, tx, loans.CreateApplicationInput{
			GroupID: groupID, ProductCode: req.ProductCode, ProductVersion: req.ProductVersion,
			Kind: req.Kind, RequestedAmountUGX: req.RequestedAmountUGX, TermMonths: req.TermMonths,
			Purpose: req.Purpose, RepaymentSource: req.RepaymentSource,
			AuthorityRecordID: req.AuthorityRecordID, AppliedBy: p.UserID.String(),
			IdempotencyKey: r.Header.Get("Idempotency-Key"),
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.application.create", EntityType: "loan_application", EntityID: loan.ID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"amount_ugx": loan.RequestedAmountUGX, "product": loan.ProductCode,
				"status": loan.Status, "loan_kind": loan.Kind},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, loanJSON(loan), nil
	})
}

func (d Deps) handleListLoanApplications(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	groupID := groupIDFrom(r)
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	out := []loanResponse{}
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		items, err := d.Loans.ListByGroup(ctx, tx, groupID, 200)
		if err != nil {
			return err
		}
		for _, l := range items {
			out = append(out, loanJSON(l))
		}
		return nil
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": out})
}

func (d Deps) handleGetLoanApplication(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionView, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	var loan loans.Loan
	err = d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		loan, err = d.Loans.Get(ctx, tx, applicationID)
		return err
	})
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, loanJSON(loan))
}

type appraiseLoanRequest struct {
	CashFlowNotes          string `json:"cash_flow_notes"`
	ExistingObligationsUGX int64  `json:"existing_obligations_ugx"`
	Recommendation         string `json:"recommendation"`
	Checks                 []struct {
		Kind   string `json:"kind"`
		Result string `json:"result"`
	} `json:"checks"`
	Guarantees []struct {
		GuarantorMemberID string `json:"guarantor_member_id"`
		AmountUGX         int64  `json:"amount_ugx"`
	} `json:"guarantees"`
}

func (d Deps) handleAppraiseLoan(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionAppraise, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req appraiseLoanRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		in := loans.AppraiseInput{
			ApplicationID: applicationID, OfficerID: p.UserID.String(),
			CashFlowNotes: req.CashFlowNotes, ExistingObligationsUGX: req.ExistingObligationsUGX,
			Recommendation: req.Recommendation,
		}
		for _, c := range req.Checks {
			in.Checks = append(in.Checks, loans.BorrowerCheckInput{Kind: c.Kind, Result: c.Result})
		}
		for _, g := range req.Guarantees {
			in.Guarantees = append(in.Guarantees, loans.GuaranteeInput{
				GuarantorMemberID: g.GuarantorMemberID, AmountUGX: g.AmountUGX,
			})
		}
		loan, err := d.Loans.Appraise(ctx, tx, in)
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.application.appraise", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"status": loan.Status, "recommendation": req.Recommendation},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, loanJSON(loan), nil
	})
}

func (d Deps) handleSendLoanToReview(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionSubmit, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, _ []byte) (int, any, error) {
		loan, err := d.Loans.SendToOperationsReview(ctx, tx, applicationID, p.UserID.String())
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.application.review_requested", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"status": loan.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, loanJSON(loan), nil
	})
}

type decideLoanRequest struct {
	Decision          string `json:"decision"`
	ApprovedAmountUGX int64  `json:"approved_amount_ugx"`
	Conditions        string `json:"conditions"`
	Reason            string `json:"reason"`
}

func (d Deps) handleDecideLoan(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionApprove, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req decideLoanRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		loan, err := d.Loans.Decide(ctx, tx, loans.DecisionInput{
			ApplicationID: applicationID, ActorID: p.UserID.String(), Decision: req.Decision,
			ApprovedAmountUGX: req.ApprovedAmountUGX, Conditions: req.Conditions, Reason: req.Reason,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.application.decide", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"decision": req.Decision, "status": loan.Status,
				"approved_amount_ugx": loan.ApprovedAmountUGX},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, loanJSON(loan), nil
	})
}

type issueOfferRequest struct {
	AmountUGX  int64 `json:"amount_ugx"`
	TermMonths int   `json:"term_months"`
	ValidDays  int   `json:"valid_days"`
}

func (d Deps) handleIssueLoanOffer(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionApprove, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req issueOfferRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		loan, err := d.Loans.IssueOffer(ctx, tx, loans.OfferInput{
			ApplicationID: applicationID, IssuedBy: p.UserID.String(),
			AmountUGX: req.AmountUGX, TermMonths: req.TermMonths, ValidDays: req.ValidDays,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.offer.issue", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"status": loan.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, loanJSON(loan), nil
	})
}

func (d Deps) handleAcceptLoanOffer(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionAcceptOffer, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, _ []byte) (int, any, error) {
		loan, err := d.Loans.AcceptOffer(ctx, tx, applicationID, p.UserID.String())
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.offer.accept", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"status": loan.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, loanJSON(loan), nil
	})
}

type disburseLoanRequest struct {
	OfferID     string `json:"offer_id"`
	AmountUGX   int64  `json:"amount_ugx"`
	DisbursedOn string `json:"disbursed_on"`
	ExternalRef string `json:"external_ref"`
}

func (d Deps) handleDisburseLoan(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionDisburse, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req disburseLoanRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		disbursedOn := time.Now().UTC()
		if req.DisbursedOn != "" {
			parsed, err := time.Parse("2006-01-02", req.DisbursedOn)
			if err != nil {
				return 0, nil, domain.Invalid("disbursed_on", "must be YYYY-MM-DD")
			}
			disbursedOn = parsed
		}
		loan, err := d.Loans.Disburse(ctx, tx, loans.DisburseInput{
			ApplicationID: applicationID, OfferID: req.OfferID, ActorID: p.UserID.String(),
			AmountUGX: req.AmountUGX, DisbursedOn: disbursedOn, ExternalRef: req.ExternalRef,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.disbursement.confirm", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"amount_ugx": req.AmountUGX, "external_ref": req.ExternalRef,
				"status": loan.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, loanJSON(loan), nil
	})
}

type materialChangeRequest struct {
	Field    string `json:"field"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	Reason   string `json:"reason"`
}

func (d Deps) handleLoanMaterialChange(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionApprove, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req materialChangeRequest
		if err := decodeStrict(body, &req); err != nil {
			return 0, nil, err
		}
		loan, err := d.Loans.RecordMaterialChange(ctx, tx, loans.MaterialChangeInput{
			ApplicationID: applicationID, ActorID: p.UserID.String(), Field: req.Field,
			OldValue: req.OldValue, NewValue: req.NewValue, Reason: req.Reason,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.application.material_change", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"field": req.Field, "status": loan.Status},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusOK, loanJSON(loan), nil
	})
}

type repaymentRequest struct {
	InstallmentID string `json:"installment_id"`
	AmountUGX     int64  `json:"amount_ugx"`
	PaidOn        string `json:"paid_on"`
	ExternalRef   string `json:"external_ref"`
}

func (d Deps) handleRecordLoanRepayment(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	applicationID := idFrom(r, "applicationID")
	groupID, err := d.loanGroup(r, applicationID)
	if err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	if err := d.authorizeGroup(r.Context(), p, rbac.ModuleLoans, rbac.ActionCreate, groupID, RequestIDFrom(r.Context())); err != nil {
		d.writeDomainError(w, r, err)
		return
	}
	d.idempotent(w, r, r.URL.Path, func(ctx context.Context, tx pgx.Tx, body []byte) (int, any, error) {
		var req repaymentRequest
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
		if err := d.Loans.RecordRepayment(ctx, tx, loans.RepaymentInput{
			ApplicationID: applicationID, InstallmentID: req.InstallmentID, ActorID: p.UserID.String(),
			AmountUGX: req.AmountUGX, PaidOn: paidOn, ExternalRef: req.ExternalRef,
		}); err != nil {
			return 0, nil, err
		}
		if err := appendAudit(ctx, tx, r, p, audit.Entry{
			Action: "loans.repayment.record", EntityType: "loan_application", EntityID: applicationID,
			ScopeType: "group", ScopeID: groupID,
			After: map[string]any{"amount_ugx": req.AmountUGX, "installment_id": req.InstallmentID},
		}); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, map[string]string{"status": "recorded"}, nil
	})
}

// loanGroup returns the group an application belongs to inside the caller's
// scope, so a caller cannot probe applications outside their bindings.
func (d Deps) loanGroup(r *http.Request, applicationID string) (string, error) {
	if applicationID == "" {
		return "", domain.ErrNotFound
	}
	var groupID string
	err := d.withActor(r, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT group_id::text FROM pamoja.loan_application WHERE id = $1::uuid`,
			applicationID).Scan(&groupID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return groupID, err
}
