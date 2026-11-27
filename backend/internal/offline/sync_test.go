package offline

import (
	"testing"
	"time"
)

// T-OFF-01: the skew is signed and reported in milliseconds.
func TestSkewMS(t *testing.T) {
	server := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if got := SkewMS(server.Add(2*time.Second), server); got != 2000 {
		t.Fatalf("a clock two seconds ahead must report +2000ms, got %d", got)
	}
	if got := SkewMS(server.Add(-500*time.Millisecond), server); got != -500 {
		t.Fatalf("a clock behind must report a negative skew, got %d", got)
	}
}

// T-OFF-02: a batch is only clean when nothing conflicted.
func TestBatchStatus(t *testing.T) {
	if BatchStatus(3, 0) != "completed" {
		t.Fatal("no conflicts means completed")
	}
	if BatchStatus(2, 1) != "completed_with_conflicts" {
		t.Fatal("a conflict must be visible in the batch status")
	}
}

// T-OFF-03: malformed batches are refused before anything is written.
func TestValidateOperations(t *testing.T) {
	valid := OperationInput{Seq: 1, Kind: KindPaymentEntry, IdempotencyKey: "k1",
		ClientCreatedAt: time.Now().UTC(), Payload: []byte(`{}`)}
	if err := ValidateOperations([]OperationInput{valid}); err != nil {
		t.Fatalf("a valid batch was refused: %v", err)
	}
	if err := ValidateOperations(nil); err == nil {
		t.Fatal("an empty batch must be refused")
	}

	unknown := valid
	unknown.Kind = "payments.delete"
	if err := ValidateOperations([]OperationInput{unknown}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}

	duplicate := valid
	duplicate.Seq = 2
	if err := ValidateOperations([]OperationInput{valid, duplicate}); err == nil {
		t.Fatal("a repeated idempotency key must be refused")
	}

	noTime := valid
	noTime.ClientCreatedAt = time.Time{}
	if err := ValidateOperations([]OperationInput{noTime}); err == nil {
		t.Fatal("a missing client time must be refused")
	}
}
