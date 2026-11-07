// Package ratelimit provides in-memory token buckets keyed by string (backend
// plan, section 8: per instance, no external store).
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a set of token buckets, one per key. Each key may burst up to
// Burst events and then refills at Rate events per second.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	sweepAt time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a Limiter allowing burst events at once and one more every
// `every` thereafter. A non-positive `every` or burst disables limiting.
func New(burst int, every time.Duration) *Limiter {
	return NewWithClock(burst, every, time.Now)
}

// NewWithClock is New with an injectable clock for tests.
func NewWithClock(burst int, every time.Duration, now func() time.Time) *Limiter {
	l := &Limiter{burst: float64(burst), now: now, buckets: map[string]*bucket{}}
	if every > 0 {
		l.rate = 1 / every.Seconds()
	}
	return l
}

// Allow takes one token for key. When none is available it returns false and
// how long until one is.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if l == nil || l.burst <= 0 || l.rate <= 0 {
		return true, 0
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// sweep drops buckets that have refilled completely (they carry no state),
// at most once a minute, so the map cannot grow without bound.
func (l *Limiter) sweep(now time.Time) {
	if now.Before(l.sweepAt) {
		return
	}
	l.sweepAt = now.Add(time.Minute)
	fullAfter := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) >= fullAfter {
			delete(l.buckets, k)
		}
	}
}

// Len returns the number of tracked keys (tests).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
