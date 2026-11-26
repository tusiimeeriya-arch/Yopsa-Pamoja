package groups

import (
	"errors"
	"testing"
	"time"
)

func TestParseDateRejectsFutureAndBadFormat(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)

	if _, err := parseDate("formed_on", "2026-12-01", true, now); !errors.Is(err, ErrValidation) {
		t.Fatalf("future date error = %v, want ErrValidation", err)
	}
	if _, err := parseDate("formed_on", "27/11/2026", true, now); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad format error = %v, want ErrValidation", err)
	}
	if _, err := parseDate("formed_on", "", true, now); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing date error = %v, want ErrValidation", err)
	}
	if parsed, err := parseDate("formed_on", "2026-11-27", true, now); err != nil {
		t.Fatalf("today rejected: %v", err)
	} else if parsed.Format("2006-01-02") != "2026-11-27" {
		t.Fatalf("parsed = %v", parsed)
	}
	// 00:30 in Kampala is still the previous day in UTC; a Ugandan "today"
	// must be accepted.
	kampalaEarly := time.Date(2026, 11, 26, 21, 30, 0, 0, time.UTC)
	if _, err := parseDate("formed_on", "2026-11-27", true, kampalaEarly); err != nil {
		t.Fatalf("Kampala today rejected: %v", err)
	}
}

func TestValidateTextBounds(t *testing.T) {
	if _, err := validateText("name", "  A  ", 2, 200); !errors.Is(err, ErrValidation) {
		t.Fatalf("short name error = %v", err)
	}
	got, err := validateText("name", "  Kampala Women's VSLA  ", 2, 200)
	if err != nil || got != "Kampala Women's VSLA" {
		t.Fatalf("trim = %q, %v", got, err)
	}
}

func TestReferenceEnums(t *testing.T) {
	for _, office := range []string{"chairperson", "secretary", "treasurer"} {
		if !validOffice(office) {
			t.Errorf("office %q rejected", office)
		}
	}
	if validOffice("president") {
		t.Error("unknown office accepted")
	}
	for _, purpose := range []string{"membership", "data_processing", "photos", "notifications"} {
		if !validConsentPurpose(purpose) {
			t.Errorf("purpose %q rejected", purpose)
		}
	}
	if validConsentPurpose("marketing") {
		t.Error("unknown purpose accepted")
	}
	if !validGender("undisclosed") || validGender("unknown") {
		t.Error("gender vocabulary mismatch")
	}
}
