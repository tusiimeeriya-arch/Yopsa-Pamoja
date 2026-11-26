package loans

import (
	"errors"
	"testing"
)

func product() Product {
	return Product{Code: "VSLA-STD", Kind: "group", MinAmountUGX: 1000000, MaxAmountUGX: 10000000,
		MaxTermMonths: 24, GuarantorAboveUGX: 500000, Version: 1}
}

func memberProduct() Product {
	return Product{Code: "MEMBER-INT", Kind: "internal_member", MinAmountUGX: 100000, MaxAmountUGX: 1000000,
		MaxTermMonths: 12, GuarantorAboveUGX: 500000, Version: 1}
}

// T-LOAN-02: 600,000 without a guarantor is blocked; 400,000 is allowed.
func TestGuarantorThreshold(t *testing.T) {
	if !GuarantorRequired(600000, 500000) {
		t.Fatal("600,000 must require a guarantor")
	}
	if GuarantorRequired(400000, 500000) {
		t.Fatal("400,000 must not require a guarantor")
	}
	if GuaranteeSufficient(600000, 400000) {
		t.Fatal("a 400,000 guarantee must not cover 600,000")
	}
	if !GuaranteeSufficient(600000, 600000) {
		t.Fatal("a 600,000 guarantee must cover 600,000")
	}
}

func TestProductLimits(t *testing.T) {
	if err := ValidateProduct(memberProduct(), Application{Kind: "internal_member", AmountUGX: 400000, TermMonths: 12}); err != nil {
		t.Fatalf("400,000 member loan rejected: %v", err)
	}
	if err := ValidateProduct(product(), Application{Kind: "group", AmountUGX: 500000, TermMonths: 12}); !errors.Is(err, ErrOutsideProduct) {
		t.Fatalf("group loan below product minimum accepted: %v", err)
	}
	if err := ValidateProduct(product(), Application{Kind: "group", AmountUGX: 5000000, TermMonths: 36}); !errors.Is(err, ErrOutsideProduct) {
		t.Fatalf("term above product maximum accepted: %v", err)
	}
	if err := ValidateProduct(product(), Application{Kind: "internal_member", AmountUGX: 5000000, TermMonths: 12}); !errors.Is(err, ErrOutsideProduct) {
		t.Fatalf("kind mismatch accepted: %v", err)
	}
}

// T-LOAN-05: self-approval is blocked, as is approval by the appraising officer.
func TestApproverIndependence(t *testing.T) {
	a := Application{ApplicantID: "chair", AppraiserID: "officer"}
	if !errors.Is(CheckApprover("chair", a), ErrApproverNotAllowed) {
		t.Fatal("applicant approved their own loan")
	}
	if !errors.Is(CheckApprover("officer", a), ErrApproverNotAllowed) {
		t.Fatal("appraiser approved the loan")
	}
	if err := CheckApprover("approver", a); err != nil {
		t.Fatalf("independent approver rejected: %v", err)
	}
}

func TestDisburserIndependence(t *testing.T) {
	a := Application{ApproverID: "approver"}
	if !errors.Is(CheckDisburser("approver", a), ErrDisburserNotAllowed) {
		t.Fatal("approver disbursed their own approval")
	}
	if err := CheckDisburser("finance", a); err != nil {
		t.Fatalf("independent Finance rejected: %v", err)
	}
}

// T-LOAN-03: a material change after approval forces re-approval.
func TestMaterialChange(t *testing.T) {
	for _, f := range []string{"amount_ugx", "term_months", "security", "guarantors", "purpose"} {
		if !IsMaterial(f) {
			t.Fatalf("%s must force re-approval", f)
		}
	}
	if IsMaterial("repayment_source") {
		t.Fatal("repayment_source is not in A-034")
	}
}

// T-LOAN-06: offer terms come only from the approved product version.
func TestOfferFromProduct(t *testing.T) {
	offer, err := OfferFromProduct(product(), 5000000, 12)
	if err != nil {
		t.Fatalf("valid offer: %v", err)
	}
	if offer.AmountUGX != 5000000 || offer.TermMonths != 12 || offer.ScheduleRef == "" {
		t.Fatalf("unexpected offer: %+v", offer)
	}
	if _, err := OfferFromProduct(product(), 500000, 12); !errors.Is(err, ErrOfferNotFromProduct) {
		t.Fatal("offer below product minimum accepted")
	}
	if _, err := OfferFromProduct(product(), 5000000, 36); !errors.Is(err, ErrOfferNotFromProduct) {
		t.Fatal("offer term above product maximum accepted")
	}
}

func TestApprovalsRequired(t *testing.T) {
	a := Authority{CreditApproverMax: 5000000, CommitteeMax: 20000000, TwoApproversAbove: 20000000}
	if ApprovalsRequired(4000000, a) != 1 {
		t.Fatal("4m should need one approver")
	}
	if ApprovalsRequired(6000000, a) != 2 {
		t.Fatal("6m should need two approvers")
	}
	if ApprovalsRequired(25000000, a) != 2 {
		t.Fatal("25m should need two approvers")
	}
}

// T-LOAN-07: group loans and internal member loans never appear in the same
// total.
func TestTotalsByKindSeparate(t *testing.T) {
	rows := []struct {
		Kind      string
		AmountUGX int64
	}{
		{"group", 5000000}, {"internal_member", 400000}, {"group", 3000000},
	}
	totals := TotalsByKind(rows)
	if len(totals) != 2 {
		t.Fatalf("got %d totals, want 2", len(totals))
	}
	if totals[0].Kind != "group" || totals[0].AmountUGX != 8000000 || totals[0].Count != 2 {
		t.Fatalf("group total wrong: %+v", totals[0])
	}
	if totals[1].Kind != "internal_member" || totals[1].AmountUGX != 400000 {
		t.Fatalf("member total wrong: %+v", totals[1])
	}
}
