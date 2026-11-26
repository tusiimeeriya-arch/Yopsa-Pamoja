// Package payments holds the pure payment and reconciliation rules. The
// database enforces the same caps and immutability; these functions make the
// decision explicit and unit-testable before any row is written.
package payments

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// Errors that callers map to a conflict or validation response.
var (
	ErrDifference          = errors.New("payments: declared and received amounts differ; full verification is blocked")
	ErrShortage            = errors.New("payments: a shortage needs a logged partial verification or a return")
	ErrOverAllocated       = errors.New("payments: allocation exceeds the confirmed funds")
	ErrEntryNotEvidenced   = errors.New("payments: every verified entry needs a source reference and evidence record")
	ErrSelfVerification    = errors.New("payments: the verifier must be independent of the recorder and submitter (SD-01/SD-02)")
	ErrCashPathSamePerson  = errors.New("payments: the cash path needs different people at each step")
	ErrEmptyPlan           = errors.New("payments: no allocation was proposed")
	ErrDuplicateAllocation = errors.New("payments: an entry may be allocated only once per receipt")
)

// EntryEvidence is the part of an entry the verification decision needs.
type EntryEvidence struct {
	EntryID   string
	AmountUGX int64
	Evidenced bool
}

// Allocation matches part of a receipt to one entry.
type Allocation struct {
	EntryID   string
	AmountUGX int64
}

// Plan is the outcome of a verification decision.
type Plan struct {
	Full        bool
	Partial     bool
	Difference  int64 // declared − received; positive is a shortage
	Total       int64
	Allocations []Allocation
}

// PlanVerification decides whether a submission may be fully or partially
// verified. A difference never produces a proportional spread: a partial plan
// allocates exactly the fully evidenced entries the verifier named.
func PlanVerification(declared, received int64, entries []EntryEvidence, requested []Allocation, allowPartial bool) (Plan, error) {
	if declared <= 0 || received <= 0 {
		return Plan{}, errors.New("payments: declared and received amounts must be positive")
	}

	if received > declared {
		return Plan{}, fmt.Errorf("%w: received %d exceeds declared %d", ErrDifference, received, declared)
	}

	if received == declared {
		plan := Plan{Full: true, Difference: 0}
		for _, e := range entries {
			if !e.Evidenced {
				return Plan{}, fmt.Errorf("%w: entry %s", ErrEntryNotEvidenced, e.EntryID)
			}
			plan.Allocations = append(plan.Allocations, Allocation{EntryID: e.EntryID, AmountUGX: e.AmountUGX})
			plan.Total += e.AmountUGX
		}
		if plan.Total != declared {
			return Plan{}, fmt.Errorf("%w: entries total %d, declared %d", ErrDifference, plan.Total, declared)
		}
		return plan, nil
	}

	// received < declared
	if !allowPartial {
		return Plan{}, fmt.Errorf("%w: difference %d", ErrShortage, declared-received)
	}
	if len(requested) == 0 {
		return Plan{}, ErrEmptyPlan
	}
	byID := make(map[string]EntryEvidence, len(entries))
	for _, e := range entries {
		byID[e.EntryID] = e
	}
	seen := map[string]bool{}
	plan := Plan{Partial: true, Difference: declared - received}
	for _, a := range requested {
		e, ok := byID[a.EntryID]
		if !ok {
			return Plan{}, fmt.Errorf("payments: entry %s is not in this submission", a.EntryID)
		}
		if seen[a.EntryID] {
			return Plan{}, ErrDuplicateAllocation
		}
		seen[a.EntryID] = true
		if a.AmountUGX <= 0 || a.AmountUGX > e.AmountUGX {
			return Plan{}, fmt.Errorf("payments: allocation for %s is outside its amount", a.EntryID)
		}
		if !e.Evidenced {
			return Plan{}, fmt.Errorf("%w: entry %s", ErrEntryNotEvidenced, a.EntryID)
		}
		plan.Allocations = append(plan.Allocations, a)
		plan.Total += a.AmountUGX
	}
	if plan.Total > received {
		return Plan{}, fmt.Errorf("%w: %d > %d", ErrOverAllocated, plan.Total, received)
	}
	return plan, nil
}

// RequireEvidence rejects a screenshot-only verification: a source reference
// and a stored evidence record are both required.
func RequireEvidence(sourceRef, evidenceID string) error {
	if sourceRef == "" || evidenceID == "" {
		return ErrEntryNotEvidenced
	}
	return nil
}

// CheckVerifier applies SD-01/SD-02.
func CheckVerifier(actorID string, involved []string) error {
	for _, id := range involved {
		if id != "" && id == actorID {
			return ErrSelfVerification
		}
	}
	return nil
}

// CheckCashPath requires the receipt recorder, handover and deposit to be three
// different people (A-017).
func CheckCashPath(verifierID, handoverBy, depositBy string) error {
	if handoverBy == "" || depositBy == "" {
		return ErrCashPathSamePerson
	}
	if verifierID == handoverBy || verifierID == depositBy || handoverBy == depositBy {
		return ErrCashPathSamePerson
	}
	return nil
}

// EntryRef is one locked entry, used to pin the entry set.
type EntryRef struct {
	ID        string
	AmountUGX int64
	MemberID  string
}

// LockHash is the deterministic hash stored on a locked submission. The
// database also verifies the declared total and count, so a client cannot
// change the set without the lock failing.
func LockHash(entries []EntryRef) string {
	ordered := append([]EntryRef(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	h := sha256.New()
	for _, e := range ordered {
		fmt.Fprintf(h, "%s|%d|%s\n", e.ID, e.AmountUGX, e.MemberID)
	}
	return hex.EncodeToString(h.Sum(nil))
}
