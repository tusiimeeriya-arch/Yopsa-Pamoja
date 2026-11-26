package audit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct {
	err  error
	scan func(dest ...any) error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return r.scan(dest...)
}

type fakeQuerier struct {
	row  pgx.Row
	sql  string
	args []any
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.sql = sql
	f.args = args
	return f.row
}

func TestAppendWritesEntry(t *testing.T) {
	fake := &fakeQuerier{row: fakeRow{scan: func(dest ...any) error {
		seq, ok := dest[0].(*int64)
		if !ok {
			t.Fatalf("scan target %T, want *int64", dest[0])
		}
		*seq = 42
		return nil
	}}}

	rec := New(fake)
	seq, err := rec.Append(context.Background(), "req-1", Entry{
		ActorUserID: "11111111-1111-1111-1111-111111111111",
		Action:      "auth.login",
		EntityType:  "session",
		EntityID:    "22222222-2222-2222-2222-222222222222",
		Before:      map[string]any{"phone": "+256700000000", "attempt": 1},
		After:       map[string]any{"status": "active"},
		IP:          "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if seq != 42 {
		t.Fatalf("seq = %d, want 42", seq)
	}
	if !strings.Contains(fake.sql, "pamoja.audit_append") {
		t.Fatalf("unexpected SQL: %s", fake.sql)
	}
	if got := fake.args[10]; got != "self" {
		t.Errorf("default channel = %v, want self", got)
	}
	before, ok := fake.args[7].(string)
	if !ok || !strings.Contains(before, "+256700000000") {
		t.Errorf("before snapshot = %v, want the caller's snapshot", fake.args[7])
	}
	if fake.args[13] != "req-1" {
		t.Errorf("correlation id = %v, want req-1", fake.args[13])
	}
}

func TestAppendNullsEmptyIdentifiers(t *testing.T) {
	fake := &fakeQuerier{row: fakeRow{scan: func(dest ...any) error {
		*dest[0].(*int64) = 1
		return nil
	}}}
	if _, err := New(fake).Append(context.Background(), "req-2", Entry{Action: "auth.login_failed"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if fake.args[0] != nil {
		t.Errorf("actor = %v, want nil", fake.args[0])
	}
	if fake.args[3] != nil || fake.args[6] != nil {
		t.Errorf("scope/entity = %v/%v, want nil", fake.args[3], fake.args[6])
	}
	if fake.args[7] != nil || fake.args[8] != nil {
		t.Errorf("snapshots = %v/%v, want nil", fake.args[7], fake.args[8])
	}
}

func TestAppendRejectsUnencodableSnapshot(t *testing.T) {
	fake := &fakeQuerier{row: fakeRow{scan: func(_ ...any) error { return nil }}}
	_, err := New(fake).Append(context.Background(), "req-3", Entry{
		Action: "auth.login",
		Before: map[string]any{"bad": make(chan int)},
	})
	if err == nil {
		t.Fatal("expected an encoding error")
	}
}

func TestAppendWrapsDatabaseError(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeQuerier{row: fakeRow{err: boom}}
	_, err := New(fake).Append(context.Background(), "req-4", Entry{Action: "auth.login"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the database error", err)
	}
}

func TestVerifyReportsIntactChain(t *testing.T) {
	fake := &fakeQuerier{row: fakeRow{scan: func(dest ...any) error {
		*dest[0].(*int64) = 12
		*dest[1].(**int64) = nil
		*dest[2].(**string) = nil
		return nil
	}}}
	result, err := New(fake).Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Intact() || result.Checked != 12 {
		t.Fatalf("result = %+v, want intact 12 rows", result)
	}
	if !strings.Contains(fake.sql, "pamoja.audit_verify") {
		t.Fatalf("unexpected SQL: %s", fake.sql)
	}
}

func TestVerifyReportsBreak(t *testing.T) {
	badSeq := int64(7)
	reason := "hash chain break at seq 7"
	fake := &fakeQuerier{row: fakeRow{scan: func(dest ...any) error {
		*dest[0].(*int64) = 6
		*dest[1].(**int64) = &badSeq
		*dest[2].(**string) = &reason
		return nil
	}}}
	result, err := New(fake).Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Intact() {
		t.Fatal("expected the chain to be reported as broken")
	}
	if result.FirstBadSeq == nil || *result.FirstBadSeq != 7 {
		t.Fatalf("first bad seq = %v, want 7", result.FirstBadSeq)
	}
	if result.Reason != reason {
		t.Fatalf("reason = %q, want %q", result.Reason, reason)
	}
}

func TestVerifyWrapsDatabaseError(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeQuerier{row: fakeRow{err: boom}}
	if _, err := New(fake).Verify(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the database error", err)
	}
}

func TestRedact(t *testing.T) {
	if Redact(nil) != nil {
		t.Fatal("Redact(nil) should be nil")
	}

	original := map[string]any{
		"phone":    "+256700000000",
		"Password": "hunter2",
		"nested": map[string]any{
			"totp_code": "123456",
			"note":      "keep me",
		},
		"codes": []any{"a", map[string]any{"refresh_token": "secret"}},
		"count": 3,
	}

	redacted := Redact(original)
	if redacted["phone"] != Redacted || redacted["Password"] != Redacted {
		t.Fatalf("top-level sensitive keys not redacted: %+v", redacted)
	}
	nested := redacted["nested"].(map[string]any)
	if nested["totp_code"] != Redacted || nested["note"] != "keep me" {
		t.Fatalf("nested redaction wrong: %+v", nested)
	}
	codes := redacted["codes"].([]any)
	if codes[1].(map[string]any)["refresh_token"] != Redacted {
		t.Fatalf("slice redaction wrong: %+v", codes)
	}
	if redacted["count"] != 3 {
		t.Fatalf("non-sensitive value changed: %v", redacted["count"])
	}
	if original["phone"] != "+256700000000" {
		t.Fatal("Redact mutated the caller's map")
	}
}
