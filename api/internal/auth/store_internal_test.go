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
