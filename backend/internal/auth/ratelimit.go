package auth

import (
	"sync"
	"time"
)

// limiter is a fixed-window counter. Login throttling is in-process: it
// protects a single API instance, and the database lockout is the durable
// second line of defence across instances.
type limiter struct {
	mu      sync.Mutex
	limit   int
	period  time.Duration
	entries map[string]*limitEntry
}

type limitEntry struct {
	count int
	reset time.Time
}

func newLimiter(limit int, period time.Duration) *limiter {
	return &limiter{
		limit:   limit,
		period:  period,
		entries: make(map[string]*limitEntry),
	}
}

// allow records an attempt and reports whether it is within the limit.
func (l *limiter) allow(key string, now time.Time) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[key]
	if !ok || !now.Before(entry.reset) {
		l.entries[key] = &limitEntry{count: 1, reset: now.Add(l.period)}
		l.pruneLocked(now)
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	return true
}

// pruneLocked drops expired entries once the map grows large, so an attacker
// cycling keys cannot exhaust memory.
func (l *limiter) pruneLocked(now time.Time) {
	if len(l.entries) < 1024 {
		return
	}
	for key, entry := range l.entries {
		if !now.Before(entry.reset) {
			delete(l.entries, key)
		}
	}
}
