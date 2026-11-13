package roster_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

var key = []byte("0123456789abcdef0123456789abcdef")

var codeRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

func TestNewCodeIsCrockfordBase32InGroupsOfFour(t *testing.T) {
	seen := map[string]bool{}
	for range 500 {
		code, err := roster.NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if !codeRE.MatchString(code) {
			t.Fatalf("code = %q, want match for %s", code, codeRE)
		}
		if seen[code] {
			t.Fatalf("code %q generated twice", code)
		}
		seen[code] = true
	}
}

func TestNormalizeCode(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"display form", "ABCD-EFGH-JKMN", "ABCDEFGHJKMN"},
		{"lowercase with spaces", " abcd efgh jkmn ", "ABCDEFGHJKMN"},
		{"look-alike letters", "oOiI-lL01-2345", "001111012345"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := roster.NormalizeCode(tc.in); got != tc.want {
				t.Fatalf("NormalizeCode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHashCodeIsHMACOfNormalizedCode(t *testing.T) {
	code, err := roster.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(strings.ReplaceAll(code, "-", "")))
	want := m.Sum(nil)
	// Round trip: what a student types later hashes to the stored value.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	for _, in := range []string{code, typed} {
		if got := roster.HashCode(key, in); !hmac.Equal(got, want) {
			t.Fatalf("HashCode(%q) is not HMAC-SHA256(key, normalized code)", in)
		}
	}
	other := []byte("fedcba9876543210fedcba9876543210")
	if hmac.Equal(roster.HashCode(other, code), want) {
		t.Fatal("hash does not depend on the key")
	}
}

func TestParseKey(t *testing.T) {
	b64 := func(n int) string { return base64.StdEncoding.EncodeToString(make([]byte, n)) }
	for _, tc := range []struct {
		name, in string
		ok       bool
	}{
		{"missing", "", false},
		{"not base64", "!!!not-base64!!!", false},
		{"31 bytes", b64(31), false},
		{"32 bytes", b64(32), true},
		{"64 bytes", b64(64), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, err := roster.ParseKey(tc.in)
			if tc.ok && (err != nil || len(k) < 32) {
				t.Fatalf("ParseKey = %d bytes, %v; want a key", len(k), err)
			}
			if !tc.ok && err == nil {
				t.Fatal("ParseKey accepted an invalid key")
			}
		})
	}
}
