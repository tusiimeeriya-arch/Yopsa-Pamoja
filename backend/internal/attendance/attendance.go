// Package attendance implements the P3 register rules: eligible meetings,
// individual and group attendance percentages, and controlled amendments.
//
// The formulas are pure so they can be unit-tested without a database:
//   - individual % = attended ÷ eligible expected meetings × 100
//   - zero expected meetings ⇒ "Not applicable", never 0 % or 100 %
//   - group % = Σ attended opportunities ÷ Σ expected opportunities, never the
//     average of individual percentages
//   - cancelled meetings and meetings before joining (or after leaving) are
//     excluded; late and excused count only when the group policy says so
//   - photos never count as attendance
package attendance

import (
	"errors"
	"sort"
	"time"
)

// Validation errors for the controlled amendment path.
var (
	ErrOriginalRequired = errors.New("attendance: original row is required")
	ErrInvalidStatus    = errors.New("attendance: invalid status")
	ErrReasonRequired   = errors.New("attendance: reason is required")
	ErrReviewerRequired = errors.New("attendance: reviewer is required")
	ErrSelfReview       = errors.New("attendance: the reviewer must differ from the recorder (SD-09)")
)

// Statuses of a meeting that matter to the formula.
const (
	MeetingCancelled = "cancelled"
)

// Attendance statuses.
const (
	StatusPresent = "present"
	StatusLate    = "late"
	StatusExcused = "excused"
	StatusAbsent  = "absent"
)

// Policy carries the two configurable attendance assumptions (A-002, A-003).
type Policy struct {
	LateCountsAsPresent    bool
	ExcusedCountsAsPresent bool
}

// DefaultPolicy is the proposed policy in ASSUMPTIONS.md: late and excused
// both count as attended, but late is still flagged.
func DefaultPolicy() Policy {
	return Policy{LateCountsAsPresent: true, ExcusedCountsAsPresent: true}
}

// Meeting is the part of a meeting the formula needs.
type Meeting struct {
	ID           string
	ScheduledFor time.Time
	Status       string
}

// Membership bounds a member's eligibility. Nil means open-ended.
type Membership struct {
	MemberID string
	JoinedOn *time.Time
	LeftOn   *time.Time
}

// Record is one attendance row. AmendmentOfID is set on a correction row.
type Record struct {
	MeetingID      string
	MemberID       string
	Status         string
	AmendmentOfID  string
	RecordedAt     time.Time
	AmendmentIndex int
}

// Result is an attendance percentage that may be "not applicable".
type Result struct {
	Expected   int
	Attended   int
	Percent    *float64
	Applicable bool
	Label      string
}

// NotApplicable is the label required when there are no expected meetings.
const NotApplicable = "Not applicable"

func result(expected, attended int) Result {
	if expected == 0 {
		return Result{Expected: 0, Attended: 0, Applicable: false, Label: NotApplicable}
	}
	pct := float64(attended) * 100 / float64(expected)
	return Result{Expected: expected, Attended: attended, Percent: &pct, Applicable: true}
}

// Eligible reports whether a meeting counts for a membership.
func Eligible(m Meeting, ms Membership) bool {
	if m.Status == MeetingCancelled {
		return false
	}
	day := dateOnly(m.ScheduledFor)
	if ms.JoinedOn != nil && day.Before(dateOnly(*ms.JoinedOn)) {
		return false
	}
	if ms.LeftOn != nil && day.After(dateOnly(*ms.LeftOn)) {
		return false
	}
	return true
}

// CountsAsAttended applies the policy to one status.
func CountsAsAttended(status string, p Policy) bool {
	switch status {
	case StatusPresent:
		return true
	case StatusLate:
		return p.LateCountsAsPresent
	case StatusExcused:
		return p.ExcusedCountsAsPresent
	default:
		return false
	}
}

// Effective returns one record per (meeting, member), with amendments replacing
// the row they correct. The caller passes rows in insertion order; the latest
// amendment wins. Original rows are never deleted.
func Effective(records []Record) map[string]Record {
	out := make(map[string]Record, len(records))
	ordered := append([]Record(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].RecordedAt.Equal(ordered[j].RecordedAt) {
			return ordered[i].AmendmentIndex < ordered[j].AmendmentIndex
		}
		return ordered[i].RecordedAt.Before(ordered[j].RecordedAt)
	})
	for _, r := range ordered {
		out[r.MeetingID+"|"+r.MemberID] = r
	}
	return out
}

// Individual computes one member's percentage across the eligible meetings.
func Individual(meetings []Meeting, ms Membership, records []Record, p Policy) Result {
	expected := 0
	attended := 0
	effective := Effective(records)
	for _, m := range meetings {
		if !Eligible(m, ms) {
			continue
		}
		expected++
		if r, ok := effective[m.ID+"|"+ms.MemberID]; ok && CountsAsAttended(r.Status, p) {
			attended++
		}
	}
	return result(expected, attended)
}

// Group computes the group percentage from total opportunities, never as the
// mean of individual percentages.
func Group(meetings []Meeting, members []Membership, records []Record, p Policy) Result {
	expected := 0
	attended := 0
	effective := Effective(records)
	for _, ms := range members {
		for _, m := range meetings {
			if !Eligible(m, ms) {
				continue
			}
			expected++
			if r, ok := effective[m.ID+"|"+ms.MemberID]; ok && CountsAsAttended(r.Status, p) {
				attended++
			}
		}
	}
	return result(expected, attended)
}

// Amendment is the controlled correction payload. The original row is kept.
type Amendment struct {
	OriginalID string
	NewStatus  string
	Reason     string
	ReviewerID string
	RecorderID string
	At         time.Time
}

// ValidateAmendment enforces the controlled path (SD-09).
func ValidateAmendment(a Amendment) error {
	if a.OriginalID == "" {
		return ErrOriginalRequired
	}
	if a.NewStatus != StatusPresent && a.NewStatus != StatusLate &&
		a.NewStatus != StatusExcused && a.NewStatus != StatusAbsent {
		return ErrInvalidStatus
	}
	if a.Reason == "" {
		return ErrReasonRequired
	}
	if a.ReviewerID == "" {
		return ErrReviewerRequired
	}
	if a.ReviewerID == a.RecorderID {
		return ErrSelfReview
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
