package auth_test

import (
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)} }

func TestLimiterBlocksSixthAttemptUntilWindowPasses(t *testing.T) {
	clk := newClock()
	l := auth.NewLimiter(5, 15*time.Minute, clk.Now)
	for i := range 5 {
		if !l.Take("k") {
			t.Fatalf("attempt %d blocked, want allowed", i+1)
		}
		clk.Advance(time.Minute)
	}
	if l.Take("k") {
		t.Fatal("6th attempt allowed, want blocked")
	}
	// The first attempt was at minute 0; at minute 15 it leaves the window.
	clk.Advance(10 * time.Minute)
	if !l.Take("k") {
		t.Fatal("attempt after the oldest left the window blocked, want allowed")
	}
	if l.Take("k") {
		t.Fatal("next attempt allowed, want blocked: only one slot was freed")
	}
}

func TestLimiterBlockedTakeRecordsNothing(t *testing.T) {
	l := auth.NewLimiter(1, time.Minute, newClock().Now)
	if !l.Take("ip", "email") {
		t.Fatal("first attempt blocked")
	}
	if l.Take("other-ip", "email") {
		t.Fatal("attempt with a blocked key allowed")
	}
	// other-ip was not charged for the rejected attempt.
	if !l.Take("other-ip", "other-email") {
		t.Fatal("other-ip blocked after a rejected attempt, want allowed")
	}
}

func TestLimiterRefundAndReset(t *testing.T) {
	l := auth.NewLimiter(2, time.Minute, newClock().Now)
	l.Take("a")
	l.Take("a")
	l.Refund("a")
	if !l.Take("a") {
		t.Fatal("blocked after Refund, want one slot back")
	}
	if l.Take("a") {
		t.Fatal("Refund returned more than one slot")
	}
	l.Reset("a")
	if !l.Take("a") || !l.Take("a") {
		t.Fatal("blocked after Reset, want all slots back")
	}
}

func TestLimiterWithMaxOverridesThePrefix(t *testing.T) {
	l := auth.NewLimiter(2, time.Minute, newClock().Now).WithMax("ip:", 4)
	for i := range 4 {
		if !l.Take("ip:a") {
			t.Fatalf("ip attempt %d blocked, want allowed (limit 4)", i+1)
		}
	}
	if l.Take("ip:a") {
		t.Fatal("5th ip attempt allowed, want blocked")
	}
	if !l.Take("email:x") || !l.Take("email:x") || l.Take("email:x") {
		t.Fatal("email keys must keep the default limit of 2")
	}
}

// The longest matching prefix sets the limit, whatever the map iteration order.
func TestLimiterLongestPrefixWins(t *testing.T) {
	for range 50 {
		l := auth.NewLimiter(5, time.Minute, newClock().Now).
			WithMax("ip:", 3).WithMax("ip:10.", 1).WithMax("i", 2)
		for _, c := range []struct {
			k    string
			want int
		}{{"ip:10.0.0.1", 1}, {"ip:192.0.2.1", 3}, {"id:x", 2}, {"email:a@b", 5}} {
			n := 0
			for l.Take(c.k) {
				n++
			}
			if n != c.want {
				t.Fatalf("%s: allowed %d, want %d", c.k, n, c.want)
			}
		}
	}
}
