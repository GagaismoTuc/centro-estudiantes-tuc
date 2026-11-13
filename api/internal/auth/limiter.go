package auth

import (
	"sync"
	"time"
)

// Limiter counts attempts per key in a sliding window. Sign-in uses one key per client IP and one
// per lowercased email.
//
// shortcut: state lives in this process's memory, so it only holds for a single server instance
// and resets on restart. Scaling out needs a shared store (PostgreSQL or Redis).
type Limiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	now       func() time.Time
	hits      map[string][]time.Time
	lastSweep time.Time
}

// NewLimiter allows max attempts per key within window.
func NewLimiter(max int, window time.Duration, now func() time.Time) *Limiter {
	return &Limiter{max: max, window: window, now: now, hits: map[string][]time.Time{}}
}

// Take records one attempt for every key and returns true when all keys are under the limit.
// When any key is at the limit it records nothing and returns false. Check and record are one
// atomic step, so concurrent requests cannot overshoot the limit.
func (l *Limiter) Take(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	for _, k := range keys {
		l.hits[k] = l.live(l.hits[k], now)
		if len(l.hits[k]) >= l.max {
			return false
		}
	}
	for _, k := range keys {
		l.hits[k] = append(l.hits[k], now)
	}
	return true
}

// Refund removes the most recent attempt of key.
func (l *Limiter) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h := l.hits[key]; len(h) > 0 {
		l.hits[key] = h[:len(h)-1]
	}
}

// Reset forgets every attempt of key.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// live drops attempts that left the window: an attempt at t counts while t > now-window.
func (l *Limiter) live(h []time.Time, now time.Time) []time.Time {
	cut := now.Add(-l.window)
	i := 0
	for i < len(h) && !h[i].After(cut) {
		i++
	}
	return h[i:]
}

// sweep drops idle keys once per window so memory stays bounded by recent traffic.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for k, h := range l.hits {
		if h = l.live(h, now); len(h) == 0 {
			delete(l.hits, k)
		} else {
			l.hits[k] = h
		}
	}
}
