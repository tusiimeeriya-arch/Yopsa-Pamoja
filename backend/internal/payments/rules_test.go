package payments

import (
	"errors"
	"testing"
)

func entries() []EntryEvidence {
	out := make([]EntryEvidence, 0, 10)
	for i := 0; i < 10; i++ {
		out = append(out, EntryEvidence{
			EntryID:   string(rune('a' + i)),
			AmountUGX: 20000,
			Evidenced: true,
		})
	}
	return out
}

// T-PAY-01: 10 × 20,000 = 200,000 declared, 180,000 received shows a 20,000
// difference; full verification is blocked and the shortage is never spread.
func TestShortageBlocksFullVerification(t *testing.T) {
	_, err := PlanVerification(200000, 180000, entries(), nil, false)
	if !errors.Is(err, ErrShortage) {
		t.Fatalf("got %v, want ErrShortage", err)
	}

	// A logged partial verification of nine fully evidenced entries is allowed.
	plan, err := PlanVerification(200000, 180000, entries(), []Allocation{
		{EntryID: "a", AmountUGX: 20000}, {EntryID: "b", AmountUGX: 20000},
		{EntryID: "c", AmountUGX: 20000}, {EntryID: "d", AmountUGX: 20000},
		{EntryID: "e", AmountUGX: 20000}, {EntryID: "f", AmountUGX: 20000},
		{EntryID: "g", AmountUGX: 20000}, {EntryID: "h", AmountUGX: 20000},
		{EntryID: "i", AmountUGX: 20000},
	}, true)
	if err != nil {
		t.Fatalf("partial verification: %v", err)
	}
	if !plan.Partial || plan.Difference != 20000 || plan.Total != 180000 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	// Nine allocations, not ten: the 20,000 shortage was not spread.
	if len(plan.Allocations) != 9 {
		t.Fatalf("shortage was spread: %d allocations", len(plan.Allocations))
	}
}

func TestPartialRequiresEvidenceOnEveryAllocatedEntry(t *testing.T) {
	es := entries()
	es[0].Evidenced = false
	_, err := PlanVerification(200000, 180000, es, []Allocation{{EntryID: "a", AmountUGX: 20000}}, true)
	if !errors.Is(err, ErrEntryNotEvidenced) {
		t.Fatalf("got %v, want evidence error", err)
	}
}

func TestAllocationCap(t *testing.T) {
	_, err := PlanVerification(200000, 180000, entries(), []Allocation{
		{EntryID: "a", AmountUGX: 20000},
		{EntryID: "b", AmountUGX: 20000},
		{EntryID: "c", AmountUGX: 20000},
		{EntryID: "d", AmountUGX: 20000},
		{EntryID: "e", AmountUGX: 20000},
		{EntryID: "f", AmountUGX: 20000},
		{EntryID: "g", AmountUGX: 20000},
		{EntryID: "h", AmountUGX: 20000},
		{EntryID: "i", AmountUGX: 20000},
		{EntryID: "j", AmountUGX: 20000},
	}, true)
	if !errors.Is(err, ErrOverAllocated) {
		t.Fatalf("got %v, want ErrOverAllocated", err)
	}
}

func TestFullVerificationRequiresEqualityAndEvidence(t *testing.T) {
	if _, err := PlanVerification(200000, 200000, entries(), nil, false); err != nil {
		t.Fatalf("full verification: %v", err)
	}
	es := entries()
	es[3].Evidenced = false
	if _, err := PlanVerification(200000, 200000, es, nil, false); !errors.Is(err, ErrEntryNotEvidenced) {
		t.Fatalf("got %v, want evidence error", err)
	}
}

func TestOverpaymentIsADifference(t *testing.T) {
	if _, err := PlanVerification(200000, 220000, entries(), nil, true); !errors.Is(err, ErrDifference) {
		t.Fatalf("got %v, want ErrDifference", err)
	}
}

// T-PAY-06: a treasurer verifying their own submission is rejected.
func TestSelfVerification(t *testing.T) {
	if err := CheckVerifier("treasurer", []string{"treasurer", "verifier"}); !errors.Is(err, ErrSelfVerification) {
		t.Fatalf("got %v, want self-verification error", err)
	}
	if err := CheckVerifier("verifier", []string{"treasurer", "recorder"}); err != nil {
		t.Fatalf("independent verifier rejected: %v", err)
	}
}

// T-PAY-07: a screenshot alone does not verify; the cash path needs different
// people at each step.
func TestEvidenceAndCashPath(t *testing.T) {
	if err := RequireEvidence("", "screenshot.png"); !errors.Is(err, ErrEntryNotEvidenced) {
		t.Fatalf("screenshot-only evidence accepted: %v", err)
	}
	if err := RequireEvidence("MM-123", "evidence-1"); err != nil {
		t.Fatalf("source + evidence rejected: %v", err)
	}
	if err := CheckCashPath("verifier", "treasurer", "treasurer"); !errors.Is(err, ErrCashPathSamePerson) {
		t.Fatalf("same handover/deposit accepted: %v", err)
	}
	if err := CheckCashPath("verifier", "treasurer", "officer"); err != nil {
		t.Fatalf("distinct cash path rejected: %v", err)
	}
}

func TestLockHashIsStableAndOrderIndependent(t *testing.T) {
	a := []EntryRef{{ID: "2", AmountUGX: 20000, MemberID: "m2"}, {ID: "1", AmountUGX: 10000, MemberID: "m1"}}
	b := []EntryRef{{ID: "1", AmountUGX: 10000, MemberID: "m1"}, {ID: "2", AmountUGX: 20000, MemberID: "m2"}}
	if LockHash(a) != LockHash(b) {
		t.Fatal("lock hash depends on input order")
	}
	b[1].AmountUGX = 20001
	if LockHash(a) == LockHash(b) {
		t.Fatal("lock hash did not change with an amount")
	}
}
