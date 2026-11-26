package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactSensitiveAttributes(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: redact})
	log := slog.New(handler)

	log.Info("login attempt",
		"user_id", "u-1",
		"password", "hunter2",
		"refresh_token", "opaque-token",
		"phone", "+256700000000",
	)

	out := buf.String()
	for _, leaked := range []string{"hunter2", "opaque-token", "+256700000000"} {
		if strings.Contains(out, leaked) {
			t.Errorf("log output leaked %q: %s", leaked, out)
		}
	}
	if !strings.Contains(out, "u-1") {
		t.Errorf("non-sensitive attribute was dropped: %s", out)
	}
	if strings.Count(out, redacted) != 3 {
		t.Errorf("expected 3 redactions, got %s", out)
	}
}

func TestNewRejectsUnknownLevel(t *testing.T) {
	if _, err := New("verbose", "development"); err == nil {
		t.Fatal("expected error for unknown level")
	}
}
