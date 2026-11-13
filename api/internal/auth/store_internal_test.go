package auth

import (
	"testing"
	"time"
)

// The dummy hash is computed when the store is built, so the first login with an unknown email
// does not pay for it (a timing outlier that would reveal the miss).
func TestNewStorePrecomputesTheDummyHash(t *testing.T) {
	s := NewStore(nil, time.Now)
	if s.dummy == "" {
		t.Fatal("dummy hash not computed at construction")
	}
	if ok, err := VerifyPassword(s.dummy, "anything"); ok || err != nil {
		t.Fatalf("VerifyPassword(dummy) = %v, %v; want a valid hash that matches nothing", ok, err)
	}
}

// A batch of zero or less would never finish (0 rows deleted is never < 0): it is rejected before
// the database is touched.
func TestDeleteExpiredSessionsRejectsNonPositiveBatch(t *testing.T) {
	s := NewStore(nil, time.Now) // a nil pool would panic if it were used
	for _, batch := range []int{0, -1} {
		n, err := s.DeleteExpiredSessions(t.Context(), batch)
		if err == nil || err.Error() != "auth: delete expired sessions: batch must be positive" || n != 0 {
			t.Fatalf("DeleteExpiredSessions(%d) = %d, %v; want 0 and the batch error", batch, n, err)
		}
	}
}

// Constructors need a clock: a nil one would only fail later, inside a request.
func TestConstructorsRejectANilClock(t *testing.T) {
	for _, tc := range []struct {
		want string
		fn   func()
	}{
		{"auth: NewStore needs a clock", func() { NewStore(nil, nil) }},
		{"auth: NewMFA needs a clock", func() { NewMFA(nil, make([]byte, 32), nil) }},
	} {
		func() {
			defer func() {
				if got := recover(); got != tc.want {
					t.Fatalf("panic = %v, want %q", got, tc.want)
				}
			}()
			tc.fn()
		}()
	}
}
