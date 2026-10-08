// Package ratelimit provides an in-memory token-bucket rate limiter keyed by
// an arbitrary string, such as a client IP address or an email address.
//
// State lives in the process, so with several API instances each enforces
// its own limits. That's fine behind a single instance or sticky load
// balancing; a shared store would be needed beyond that.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter allows each key up to burst events at once, refilled at r per
// second.
type Limiter struct {
	mu      sync.Mutex
	r       rate.Limit
	burst   int
	buckets map[string]*rate.Limiter
	now     func() time.Time // for tests
}

// New returns a Limiter allowing burst events at once and r events per
// second after that, per key.
func New(r rate.Limit, burst int) *Limiter {
	return &Limiter{
		r:       r,
		burst:   burst,
		buckets: make(map[string]*rate.Limiter),
		now:     time.Now,
	}
}

// Per returns the rate of n events per interval, e.g. Per(10, time.Minute).
func Per(n int, interval time.Duration) rate.Limit {
	return rate.Every(interval / time.Duration(n))
}

// Allow records an event for key. If the key is over its limit, it returns
// false and how long until the next event would be allowed.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = rate.NewLimiter(l.r, l.burst)
		l.buckets[key] = b
	}

	now := l.now()
	reservation := b.ReserveN(now, 1)
	if delay := reservation.DelayFrom(now); delay > 0 {
		// Don't count a refused event against the key.
		reservation.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// Cleanup forgets keys whose bucket has refilled completely: they behave
// exactly like keys never seen, so no limit is lost. Call it periodically.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	for key, b := range l.buckets {
		if b.TokensAt(now) >= float64(l.burst) {
			delete(l.buckets, key)
		}
	}
}

// Len returns the number of keys being tracked.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
