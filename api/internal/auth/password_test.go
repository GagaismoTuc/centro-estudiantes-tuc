package auth_test

import (
	"strings"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	const pw = "correct horse battery staple"
	h1, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	h2, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	const prefix = "$argon2id$v=19$m=19456,t=2,p=1$"
	if !strings.HasPrefix(h1, prefix) {
		t.Fatalf("hash = %q, want PHC prefix %q", h1, prefix)
	}
	if parts := strings.Split(h1, "$"); len(parts) != 6 || len(parts[4]) != 22 || len(parts[5]) != 43 {
		t.Fatalf("hash = %q, want 16-byte salt and 32-byte key in unpadded base64", h1)
	}
	if h1 == h2 {
		t.Fatal("two hashes of the same password are equal: salt is not random")
	}
	for _, tc := range []struct {
		name, hash, pw string
		want           bool
	}{
		{"right password", h1, pw, true},
		{"right password, second hash", h2, pw, true},
		{"wrong password", h1, pw + "!", false},
		{"empty password", h1, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := auth.VerifyPassword(tc.hash, tc.pw)
			if err != nil {
				t.Fatalf("VerifyPassword: %v", err)
			}
			if ok != tc.want {
				t.Fatalf("VerifyPassword = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA",
		"$argon2id$v=19$m=19456,t=0,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$***$aGFzaA",
	} {
		t.Run(h, func(t *testing.T) {
			ok, err := auth.VerifyPassword(h, "anything")
			if err == nil || ok {
				t.Fatalf("VerifyPassword = %v, %v; want false and an error", ok, err)
			}
		})
	}
}

// One rule for bootstrap, claim and reset: 10 to 128 characters, counted as Unicode code points.
func TestValidatePassword(t *testing.T) {
	for _, tc := range []struct {
		pw   string
		want error
	}{
		{"123456789", auth.ErrWeakPassword},
		{strings.Repeat("ñ", 9), auth.ErrWeakPassword},
		{"1234567890", nil},
		{strings.Repeat("ñ", 10), nil},
		{strings.Repeat("a", 128), nil},
		{strings.Repeat("a", 129), auth.ErrWeakPassword},
	} {
		if got := auth.ValidatePassword(tc.pw); got != tc.want {
			t.Errorf("ValidatePassword(%d runes) = %v, want %v", len([]rune(tc.pw)), got, tc.want)
		}
	}
	if auth.MinPasswordLen != 10 {
		t.Fatalf("MinPasswordLen = %d, want 10", auth.MinPasswordLen)
	}
}
