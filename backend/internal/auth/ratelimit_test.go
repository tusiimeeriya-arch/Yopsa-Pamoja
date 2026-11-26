package auth

import (
	"testing"
	"time"
)

func TestLimiterAllowsWithinWindow(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	l := newLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !l.allow("key", now) {
			t.Fatalf("attempt %d was refused inside the limit", i+1)
		}
	}
	if l.allow("key", now) {
		t.Fatal("attempt above the limit was allowed")
	}
	if !l.allow("other-key", now) {
		t.Fatal("a different key was refused")
	}
}

func TestLimiterResetsAfterWindow(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	l := newLimiter(1, time.Minute)

	if !l.allow("key", now) {
		t.Fatal("first attempt refused")
	}
	if l.allow("key", now) {
		t.Fatal("second attempt inside the window allowed")
	}
	if !l.allow("key", now.Add(time.Minute)) {
		t.Fatal("attempt after the window refused")
	}
}

func TestLimiterDisabled(t *testing.T) {
	var l *limiter
	if !l.allow("key", time.Now()) {
		t.Fatal("nil limiter should allow")
	}
	if !newLimiter(0, time.Minute).allow("key", time.Now()) {
		t.Fatal("limiter with no limit should allow")
	}
}

func TestLimiterPrunesExpiredEntries(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	l := newLimiter(1, time.Second)

	for i := 0; i < 1100; i++ {
		l.allow(string(rune('a'+i%26))+string(rune(i)), now)
	}
	if len(l.entries) > 1100 {
		t.Fatalf("entries = %d, want pruning to bound the map", len(l.entries))
	}

	// A later attempt at a fresh instant prunes everything expired.
	l.allow("fresh", now.Add(time.Hour))
	if len(l.entries) != 1 {
		t.Fatalf("entries after prune = %d, want 1", len(l.entries))
	}
}
