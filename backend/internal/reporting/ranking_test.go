package reporting

import (
	"math"
	"testing"
)

// T-RANK-01: the documented weights produce the expected weighted score.
func TestComputeScoreWeights(t *testing.T) {
	w := Weights{OnTime: 50, Arrears: 25, Attendance: 15, Records: 10}
	score := ComputeScore(w, Metrics{
		OnTimePct: 100, ArrearsPct: 0, AttendancePct: 100, RecordsPct: 100,
	})
	if score.Total != 100 {
		t.Fatalf("a perfect group must score 100, got %v", score.Total)
	}
	if score.Components["on_time"] != 100 {
		t.Fatalf("components not reported: %+v", score.Components)
	}
}

// T-RANK-02: arrears lower the score, they never raise it.
func TestArrearsLowerTheScore(t *testing.T) {
	w := Weights{OnTime: 50, Arrears: 25, Attendance: 15, Records: 10}
	clean := ComputeScore(w, Metrics{OnTimePct: 80, ArrearsPct: 0, AttendancePct: 80, RecordsPct: 80})
	late := ComputeScore(w, Metrics{OnTimePct: 80, ArrearsPct: 40, AttendancePct: 80, RecordsPct: 80})
	if late.Total >= clean.Total {
		t.Fatalf("arrears must lower the score: clean %v, late %v", clean.Total, late.Total)
	}
	want := clean.Total - 40*25/100.0
	if math.Abs(late.Total-want) > 0.01 {
		t.Fatalf("arrears weight applied wrongly: got %v want %v", late.Total, want)
	}
}

// T-RANK-03: weights are normalised, not assumed to sum to 100.
func TestWeightsAreNormalised(t *testing.T) {
	half := ComputeScore(Weights{OnTime: 1}, Metrics{OnTimePct: 80})
	if half.Total != 80 {
		t.Fatalf("a single weight must normalise to its own metric, got %v", half.Total)
	}
	zero := ComputeScore(Weights{}, Metrics{OnTimePct: 80})
	if zero.Total != 0 {
		t.Fatalf("no weights means no score, got %v", zero.Total)
	}
}

// T-RANK-04: out-of-range metrics are clamped, not wrapped.
func TestMetricsAreClamped(t *testing.T) {
	score := ComputeScore(Weights{OnTime: 50, Arrears: 50},
		Metrics{OnTimePct: 250, ArrearsPct: -40})
	if score.Total != 100 {
		t.Fatalf("clamped metrics must score 100, got %v", score.Total)
	}
}

func TestParsePolicyFallsBackToDefaults(t *testing.T) {
	p, err := ParsePolicy(nil, 3)
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if p.WindowDays != 90 || p.MinMeetings != 4 || p.Version != 3 {
		t.Fatalf("defaults not applied: %+v", p)
	}
	p, err = ParsePolicy([]byte(`{"window_days":30,"min_loans":2,"min_meetings":1,"weights":{"on_time":40,"arrears":30,"attendance":20,"records":10}}`), 2)
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	if p.WindowDays != 30 || p.MinLoans != 2 || p.Weights.OnTime != 40 || p.Version != 2 {
		t.Fatalf("stored policy not honoured: %+v", p)
	}
}
