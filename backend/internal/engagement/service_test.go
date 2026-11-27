package engagement

import "testing"

// T-ENG-01: comment bodies are trimmed and bounded.
func TestCommentBodyValidation(t *testing.T) {
	if err := ValidateCommentBody("   "); err == nil {
		t.Fatal("an empty comment must be rejected")
	}
	if err := ValidateCommentBody("  The register is missing two members.  "); err != nil {
		t.Fatalf("a normal comment must be accepted: %v", err)
	}
	long := make([]byte, MaxCommentLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidateCommentBody(string(long)); err == nil {
		t.Fatal("an oversized comment must be rejected")
	}
}

// T-ENG-02: only the documented dispute transitions are allowed.
func TestDisputeTransitions(t *testing.T) {
	allowed := [][2]string{
		{"open", "investigating"}, {"open", "resolved"}, {"open", "rejected"},
		{"investigating", "resolved"}, {"investigating", "rejected"},
	}
	for _, pair := range allowed {
		if !CanTransitionDispute(pair[0], pair[1]) {
			t.Fatalf("%s -> %s must be allowed", pair[0], pair[1])
		}
	}
	denied := [][2]string{
		{"resolved", "open"}, {"rejected", "investigating"}, {"open", "open"},
	}
	for _, pair := range denied {
		if CanTransitionDispute(pair[0], pair[1]) {
			t.Fatalf("%s -> %s must be refused", pair[0], pair[1])
		}
	}
	if !RequiresResolutionNote("resolved") || RequiresResolutionNote("investigating") {
		t.Fatal("only a closing status requires a resolution note")
	}
}
