// Package reporting builds the management dashboard, the group ranking and the
// audited exports. Every number here is derived from the ledger, the register
// and the loan schedule; nothing is typed in by hand.
package reporting

import (
	"encoding/json"
	"math"
)

// Weights are the ranking policy weights (A-030). They need not sum to 100:
// the score normalises them.
type Weights struct {
	OnTime     int `json:"on_time"`
	Arrears    int `json:"arrears"`
	Attendance int `json:"attendance"`
	Records    int `json:"records"`
}

// Metrics are one group's measured inputs, each 0..100.
type Metrics struct {
	OnTimePct     float64
	SettledPct    float64
	ArrearsPct    float64
	AttendancePct float64
	RecordsPct    float64
}

// Score is the weighted result and its parts, so a drill-down can explain it.
type Score struct {
	Total      float64            `json:"total"`
	Components map[string]float64 `json:"components"`
}

// ComputeScore applies the policy weights to the measured metrics. The result
// is 0..100 rounded to two decimals. Arrears are inverted: a higher arrears
// rate lowers the score.
func ComputeScore(w Weights, m Metrics) Score {
	totalWeight := w.OnTime + w.Arrears + w.Attendance + w.Records
	if totalWeight <= 0 {
		return Score{Total: 0, Components: map[string]float64{}}
	}

	onTime := clamp(m.OnTimePct)
	arrears := clamp(m.ArrearsPct)
	attendance := clamp(m.AttendancePct)
	records := clamp(m.RecordsPct)

	weighted := onTime*float64(w.OnTime) +
		(100-arrears)*float64(w.Arrears) +
		attendance*float64(w.Attendance) +
		records*float64(w.Records)

	total := weighted / float64(totalWeight)
	return Score{
		Total: math.Round(total*100) / 100,
		Components: map[string]float64{
			"on_time":    round2(onTime),
			"arrears":    round2(arrears),
			"attendance": round2(attendance),
			"records":    round2(records),
		},
	}
}

func clamp(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Policy is the versioned ranking configuration (A-030).
type Policy struct {
	WindowDays  int     `json:"window_days"`
	MinLoans    int     `json:"min_loans"`
	MinMeetings int     `json:"min_meetings"`
	Weights     Weights `json:"weights"`
	Version     int     `json:"version"`
}

// DefaultPolicy is the documented fallback when the configuration row is
// missing; it matches the seeded A-030 value.
func DefaultPolicy() Policy {
	return Policy{
		WindowDays: 90, MinLoans: 1, MinMeetings: 4,
		Weights: Weights{OnTime: 50, Arrears: 25, Attendance: 15, Records: 10},
		Version: 1,
	}
}

// ParsePolicy reads the stored configuration, falling back to the default for
// any field the row omits.
func ParsePolicy(raw []byte, version int) (Policy, error) {
	p := DefaultPolicy()
	p.Version = version
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Policy{}, err
	}
	p.Version = version
	return p, nil
}
