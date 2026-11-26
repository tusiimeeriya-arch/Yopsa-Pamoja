// Package loans holds the borrowing rules that are independent of storage:
// product limits, the guarantor threshold, approval authority, material
// change, and the hard separation between approval and disbursement.
package loans

import (
	"errors"
	"fmt"
	"sort"
)

// Errors returned by the rule functions.
var (
	ErrOutsideProduct      = errors.New("loans: amount or term is outside the approved product version")
	ErrGuarantorRequired   = errors.New("loans: a loan above the threshold needs a checked guarantee")
	ErrApproverNotAllowed  = errors.New("loans: the approver may not be the applicant or appraiser (SD-03)")
	ErrDisburserNotAllowed = errors.New("loans: the disburser must differ from the approver (SD-04)")
	ErrMaterialChange      = errors.New("loans: material change requires re-approval")
	ErrOfferNotFromProduct = errors.New("loans: offer terms must come from the approved product version")
)

// Product is an approved loan product version.
type Product struct {
	Code              string
	Kind              string // group | internal_member
	MinAmountUGX      int64
	MaxAmountUGX      int64
	MaxTermMonths     int
	GuarantorAboveUGX int64
	Version           int
}

// Application is the part of an application the rules need.
type Application struct {
	Kind           string
	AmountUGX      int64
	TermMonths     int
	ApplicantID    string
	AppraiserID    string
	ApproverID     string
	ProductVersion int
}

// ValidateProduct checks the requested amount and term against the product
// version that priced the application.
func ValidateProduct(p Product, a Application) error {
	if p.Kind != "" && a.Kind != "" && p.Kind != a.Kind {
		return fmt.Errorf("%w: product kind %s, application kind %s", ErrOutsideProduct, p.Kind, a.Kind)
	}
	if a.AmountUGX < p.MinAmountUGX || a.AmountUGX > p.MaxAmountUGX {
		return fmt.Errorf("%w: amount %d outside %d..%d", ErrOutsideProduct, a.AmountUGX, p.MinAmountUGX, p.MaxAmountUGX)
	}
	if a.TermMonths < 1 || a.TermMonths > p.MaxTermMonths {
		return fmt.Errorf("%w: term %d outside 1..%d", ErrOutsideProduct, a.TermMonths, p.MaxTermMonths)
	}
	return nil
}

// GuarantorRequired reports whether the amount is above the configured
// threshold (A-010, default 500,000).
func GuarantorRequired(amount, threshold int64) bool {
	return amount > threshold
}

// GuaranteeSufficient checks that checked guarantees cover the amount.
func GuaranteeSufficient(amount, guaranteed int64) bool {
	return guaranteed >= amount
}

// Authority is the approval-authority configuration (A-012).
type Authority struct {
	CreditApproverMax int64
	CommitteeMax      int64
	TwoApproversAbove int64
}

// ApprovalsRequired returns 1 or 2. A single credit approver may approve up to
// CreditApproverMax; above that two independent credit approvers are needed.
func ApprovalsRequired(amount int64, a Authority) int {
	if amount > a.CreditApproverMax {
		return 2
	}
	return 1
}

// CheckApprover applies SD-03.
func CheckApprover(actorID string, a Application) error {
	if actorID == "" || actorID == a.ApplicantID || actorID == a.AppraiserID {
		return ErrApproverNotAllowed
	}
	return nil
}

// CheckDisburser applies SD-04.
func CheckDisburser(actorID string, a Application) error {
	if actorID == "" || actorID == a.ApproverID {
		return ErrDisburserNotAllowed
	}
	return nil
}

// MaterialFields are the fields whose change forces re-approval (A-034).
var MaterialFields = map[string]bool{
	"amount_ugx":  true,
	"term_months": true,
	"security":    true,
	"guarantors":  true,
	"purpose":     true,
}

// IsMaterial reports whether a field change forces re-approval.
func IsMaterial(field string) bool { return MaterialFields[field] }

// Offer is the terms issued from an approved product version.
type Offer struct {
	AmountUGX   int64
	TermMonths  int
	ScheduleRef string
}

// OfferFromProduct builds an offer only from the approved product version. It
// never infers a term or amount from another product.
func OfferFromProduct(p Product, approvedAmount int64, termMonths int) (Offer, error) {
	if approvedAmount < p.MinAmountUGX || approvedAmount > p.MaxAmountUGX {
		return Offer{}, fmt.Errorf("%w: approved amount outside product", ErrOfferNotFromProduct)
	}
	if termMonths < 1 || termMonths > p.MaxTermMonths {
		return Offer{}, fmt.Errorf("%w: term outside product", ErrOfferNotFromProduct)
	}
	return Offer{
		AmountUGX: approvedAmount, TermMonths: termMonths,
		ScheduleRef: fmt.Sprintf("%s-v%d-%d-%d", p.Code, p.Version, approvedAmount, termMonths),
	}, nil
}

// KindTotal keeps group loans and internal member loans separate.
type KindTotal struct {
	Kind      string
	Count     int
	AmountUGX int64
}

// TotalsByKind sums confirmed disbursements by loan kind. A group loan and an
// internal member loan are never added into the same total.
func TotalsByKind(rows []struct {
	Kind      string
	AmountUGX int64
}) []KindTotal {
	byKind := map[string]*KindTotal{}
	for _, r := range rows {
		if byKind[r.Kind] == nil {
			byKind[r.Kind] = &KindTotal{Kind: r.Kind}
		}
		byKind[r.Kind].Count++
		byKind[r.Kind].AmountUGX += r.AmountUGX
	}
	out := make([]KindTotal, 0, len(byKind))
	for _, v := range byKind {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
