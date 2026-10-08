package ratelimit

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(n int, interval time.Duration) (*Limiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	l := New(Per(n, interval), n)
	l.now = clock.now
	return l, clock
}

func TestAllow(t *testing.T) {
	l, clock := newTestLimiter(3, time.Minute) // 3 at once, then one every 20s

	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("event %d refused", i+1)
		}
	}

	ok, retry := l.Allow("a")
	if ok || retry != 20*time.Second {
		t.Errorf("4th event = %t, retry after %v; want refused, 20s", ok, retry)
	}

	// Refused events don't count: after 20s exactly one more is allowed.
	clock.advance(20 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("refused after refill")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Error("allowed more than refilled")
	}

	// Keys are independent.
	if ok, _ := l.Allow("b"); !ok {
		t.Error("another key was limited")
	}
}

func TestCleanupOnlyForgetsFullBuckets(t *testing.T) {
	l, clock := newTestLimiter(2, time.Minute)

	l.Allow("busy")
	l.Allow("busy")
	l.Allow("light")

	clock.advance(30 * time.Second) // light is full again; busy has 1 of 2
	l.Cleanup()
	if l.Len() != 1 {
		t.Fatalf("tracking %d keys; want only the busy one", l.Len())
	}

	// Cleanup must not have reset busy's limit.
	l.Allow("busy")
	if ok, _ := l.Allow("busy"); ok {
		t.Error("cleanup reset a partially used bucket")
	}

	clock.advance(time.Hour)
	l.Cleanup()
	if l.Len() != 0 {
		t.Errorf("tracking %d keys after everything refilled; want 0", l.Len())
	}
}

func TestConcurrentUse(t *testing.T) {
	l := New(Per(100, time.Hour), 100)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 200 {
		wg.Go(func() {
			if ok, _ := l.Allow("k"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if allowed != 100 {
		t.Errorf("allowed %d of 200; want exactly the burst of 100", allowed)
	}
}
