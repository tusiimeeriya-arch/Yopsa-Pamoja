package attendance

import (
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(t time.Time) *time.Time { return &t }

func meetings(n int, start string) []Meeting {
	out := make([]Meeting, 0, n)
	base := day(start)
	for i := 0; i < n; i++ {
		out = append(out, Meeting{ID: string(rune('A' + i)), ScheduledFor: base.AddDate(0, 0, i*7), Status: "closed"})
	}
	return out
}

// T-ATT-01: 6 of 8 meetings = 75 %.
func TestIndividualSixOfEight(t *testing.T) {
	ms := []Meeting{{ID: "1", ScheduledFor: day("2026-01-01"), Status: "closed"},
		{ID: "2", ScheduledFor: day("2026-01-08"), Status: "closed"},
		{ID: "3", ScheduledFor: day("2026-01-15"), Status: "closed"},
		{ID: "4", ScheduledFor: day("2026-01-22"), Status: "closed"},
		{ID: "5", ScheduledFor: day("2026-01-29"), Status: "closed"},
		{ID: "6", ScheduledFor: day("2026-02-05"), Status: "closed"},
		{ID: "7", ScheduledFor: day("2026-02-12"), Status: "closed"},
		{ID: "8", ScheduledFor: day("2026-02-19"), Status: "closed"}}
	records := []Record{}
	for _, id := range []string{"1", "2", "3", "4", "5", "6"} {
		records = append(records, Record{MeetingID: id, MemberID: "m1", Status: StatusPresent})
	}
	for _, id := range []string{"7", "8"} {
		records = append(records, Record{MeetingID: id, MemberID: "m1", Status: StatusAbsent})
	}
	got := Individual(ms, Membership{MemberID: "m1", JoinedOn: ptr(day("2025-12-01"))}, records, DefaultPolicy())
	if !got.Applicable || got.Expected != 8 || got.Attended != 6 || got.Percent == nil || *got.Percent != 75 {
		t.Fatalf("got %+v, want 6/8 = 75%%", got)
	}
}

// T-ATT-02: zero expected meetings is "Not applicable", never 0 or 100.
func TestIndividualNoExpectedMeetings(t *testing.T) {
	got := Individual(nil, Membership{MemberID: "m1"}, nil, DefaultPolicy())
	if got.Applicable || got.Label != NotApplicable || got.Percent != nil {
		t.Fatalf("got %+v, want Not applicable", got)
	}
}

// T-ATT-03: cancelled meetings, meetings before joining and meetings after
// leaving are excluded; a member who left mid-period is handled correctly.
func TestEligibilityExclusions(t *testing.T) {
	ms := []Meeting{
		{ID: "before", ScheduledFor: day("2025-12-01"), Status: "closed"},
		{ID: "cancelled", ScheduledFor: day("2026-01-08"), Status: MeetingCancelled},
		{ID: "inside", ScheduledFor: day("2026-01-15"), Status: "closed"},
		{ID: "left", ScheduledFor: day("2026-02-01"), Status: "closed"},
		{ID: "after", ScheduledFor: day("2026-02-15"), Status: "closed"},
	}
	records := []Record{
		{MeetingID: "inside", MemberID: "m1", Status: StatusPresent},
		{MeetingID: "left", MemberID: "m1", Status: StatusPresent},
		{MeetingID: "after", MemberID: "m1", Status: StatusPresent},
	}
	got := Individual(ms, Membership{MemberID: "m1", JoinedOn: ptr(day("2026-01-01")), LeftOn: ptr(day("2026-02-01"))}, records, DefaultPolicy())
	if got.Expected != 2 || got.Attended != 2 {
		t.Fatalf("got %+v, want 2/2 (only inside and left)", got)
	}
}

// T-ATT-04: group % uses total opportunities, never the average of individual
// percentages.
func TestGroupUsesTotalOpportunities(t *testing.T) {
	ms := []Meeting{
		{ID: "1", ScheduledFor: day("2026-01-01"), Status: "closed"},
		{ID: "2", ScheduledFor: day("2026-01-08"), Status: "closed"},
	}
	members := []Membership{
		{MemberID: "a", JoinedOn: ptr(day("2026-01-01"))},
		{MemberID: "b", JoinedOn: ptr(day("2026-01-01"))},
		{MemberID: "c", JoinedOn: ptr(day("2026-01-01"))},
	}
	// a attends both (100 %), b attends one (50 %), c attends none (0 %).
	records := []Record{
		{MeetingID: "1", MemberID: "a", Status: StatusPresent},
		{MeetingID: "2", MemberID: "a", Status: StatusPresent},
		{MeetingID: "1", MemberID: "b", Status: StatusPresent},
		{MeetingID: "2", MemberID: "b", Status: StatusAbsent},
		{MeetingID: "1", MemberID: "c", Status: StatusAbsent},
		{MeetingID: "2", MemberID: "c", Status: StatusAbsent},
	}
	got := Group(ms, members, records, DefaultPolicy())
	// 3 attended of 6 opportunities = 50 %, not (100+50+0)/3 = 50 % here by
	// coincidence; use a second shape to make the difference explicit.
	if got.Expected != 6 || got.Attended != 3 || got.Percent == nil || *got.Percent != 50 {
		t.Fatalf("got %+v, want 3/6 = 50%%", got)
	}

	// a attends both, b and c attend none: opportunities 6, attended 2.
	records = records[:2]
	got = Group(ms, members, records, DefaultPolicy())
	// Mean of individual percentages would be (100+0+0)/3 = 33.3 %.
	if got.Percent == nil || *got.Percent != 100.0*2/6 {
		t.Fatalf("got %+v, want 2/6 = 33.33%% from opportunities", got)
	}
}

func TestLateAndExcusedFollowPolicy(t *testing.T) {
	ms := []Meeting{{ID: "1", ScheduledFor: day("2026-01-01"), Status: "closed"}}
	records := []Record{{MeetingID: "1", MemberID: "m", Status: StatusLate}}
	p := Policy{LateCountsAsPresent: false, ExcusedCountsAsPresent: true}
	if got := Individual(ms, Membership{MemberID: "m"}, records, p); got.Attended != 0 {
		t.Fatalf("late must not count when policy says so: %+v", got)
	}
	records[0].Status = StatusExcused
	if got := Individual(ms, Membership{MemberID: "m"}, records, p); got.Attended != 1 {
		t.Fatalf("excused must count when policy says so: %+v", got)
	}
}

func TestAmendmentSupersedesOriginal(t *testing.T) {
	ms := []Meeting{{ID: "1", ScheduledFor: day("2026-01-01"), Status: "closed"}}
	records := []Record{
		{MeetingID: "1", MemberID: "m", Status: StatusAbsent, RecordedAt: day("2026-01-01")},
		{MeetingID: "1", MemberID: "m", Status: StatusPresent, AmendmentOfID: "orig", RecordedAt: day("2026-01-02"), AmendmentIndex: 1},
	}
	if got := Individual(ms, Membership{MemberID: "m"}, records, DefaultPolicy()); got.Attended != 1 {
		t.Fatalf("amendment must win: %+v", got)
	}
}

func TestValidateAmendment(t *testing.T) {
	valid := Amendment{OriginalID: "o", NewStatus: StatusPresent, Reason: "wrong mark", ReviewerID: "r", RecorderID: "x"}
	if err := ValidateAmendment(valid); err != nil {
		t.Fatalf("valid amendment rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Amendment)
		want error
	}{
		{"no original", func(a *Amendment) { a.OriginalID = "" }, ErrOriginalRequired},
		{"bad status", func(a *Amendment) { a.NewStatus = "maybe" }, ErrInvalidStatus},
		{"no reason", func(a *Amendment) { a.Reason = "" }, ErrReasonRequired},
		{"no reviewer", func(a *Amendment) { a.ReviewerID = "" }, ErrReviewerRequired},
		{"self review", func(a *Amendment) { a.ReviewerID = a.RecorderID }, ErrSelfReview},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := valid
			c.mut(&a)
			if err := ValidateAmendment(a); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}
