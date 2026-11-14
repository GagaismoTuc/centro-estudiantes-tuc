package totp_test

import (
	"bytes"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/totp"
)

// rfcSecret is the SHA-1 seed of RFC 6238 Appendix B.
var rfcSecret = []byte("12345678901234567890")

// RFC 6238 Appendix B, SHA-1 column, truncated to the last 6 digits (RFC 4226 truncation mod 10^6).
func TestCodeMatchesRFC6238Vectors(t *testing.T) {
	for _, tc := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		step := totp.StepAt(time.Unix(tc.unix, 0))
		if got := totp.Code(rfcSecret, step); got != tc.want {
			t.Errorf("Code(T=%d, step %d) = %s, want %s", tc.unix, step, got, tc.want)
		}
	}
}

func TestVerifyAcceptsOneStepOfDriftAndRejectsReplays(t *testing.T) {
	now := time.Unix(1111111111, 0)
	cur := totp.StepAt(now)
	for _, tc := range []struct {
		name     string
		step     int64
		lastUsed int64
		ok       bool
	}{
		{"current", cur, -1, true},
		{"previous", cur - 1, -1, true},
		{"next", cur + 1, -1, true},
		{"two back", cur - 2, -1, false},
		{"two ahead", cur + 2, -1, false},
		{"replay of the last used step", cur, cur, false},
		{"older than the last used step", cur - 1, cur, false},
		{"newer than the last used step", cur + 1, cur, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := totp.Verify(rfcSecret, totp.Code(rfcSecret, tc.step), now, tc.lastUsed)
			if ok != tc.ok || (ok && got != tc.step) {
				t.Fatalf("Verify = (%d, %v), want (%d, %v)", got, ok, tc.step, tc.ok)
			}
		})
	}
	for _, bad := range []string{"", "12345", "1234567", "12345a", " 050471", "０５０４７１"} {
		if _, ok := totp.Verify(rfcSecret, bad, now, -1); ok {
			t.Errorf("Verify(%q) = ok, want rejected", bad)
		}
	}
}

func TestNewSecretAndURI(t *testing.T) {
	a, err := totp.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := totp.NewSecret()
	if len(a) != 20 || bytes.Equal(a, b) {
		t.Fatalf("secrets %x / %x: want 20 random bytes each", a, b)
	}
	enc := totp.EncodeSecret(a)
	if strings.Contains(enc, "=") || len(enc) != 32 {
		t.Fatalf("EncodeSecret = %q, want 32 base32 characters without padding", enc)
	}
	if dec, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enc); err != nil || !bytes.Equal(dec, a) {
		t.Fatalf("EncodeSecret does not round-trip: %x %v", dec, err)
	}
	const want = "otpauth://totp/Centro%20de%20Estudiantes:ana@example.com?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" +
		"&issuer=Centro%20de%20Estudiantes&algorithm=SHA1&digits=6&period=30"
	if got := totp.URI("ana@example.com", rfcSecret); got != want {
		t.Fatalf("URI = %s\nwant  %s", got, want)
	}
	// An address cannot inject extra parameters or path segments.
	if got := totp.URI("a?secret=X&b/c@example.com", rfcSecret); !strings.HasPrefix(got,
		"otpauth://totp/Centro%20de%20Estudiantes:a%3Fsecret=X&b%2Fc@example.com?secret=GEZ") {
		t.Fatalf("URI did not escape the account: %s", got)
	}
}

func TestSealOpenRoundTripAndTamperRejection(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	const user = "6f1c1c3e-1111-4222-8333-444455556666"
	sealed, err := totp.Seal(key, user, rfcSecret)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := totp.Seal(key, user, rfcSecret)
	if bytes.Equal(sealed, again) {
		t.Fatal("two seals are identical: nonce is not random")
	}
	if bytes.Contains(sealed, rfcSecret) || len(sealed) != 12+20+16 {
		t.Fatalf("sealed = %x (%d bytes), want nonce+ciphertext+tag without the plaintext", sealed, len(sealed))
	}
	if got, err := totp.Open(key, user, sealed); err != nil || !bytes.Equal(got, rfcSecret) {
		t.Fatalf("Open = %q, %v; want the secret", got, err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	other := bytes.Repeat([]byte{8}, 32)
	for name, open := range map[string]func() ([]byte, error){
		"flipped bit":      func() ([]byte, error) { return totp.Open(key, user, tampered) },
		"other user (AAD)": func() ([]byte, error) { return totp.Open(key, "00000000-0000-4000-8000-000000000000", sealed) },
		"other key":        func() ([]byte, error) { return totp.Open(other, user, sealed) },
		"truncated":        func() ([]byte, error) { return totp.Open(key, user, sealed[:10]) },
	} {
		if got, err := open(); !errors.Is(err, totp.ErrDecrypt) || got != nil {
			t.Errorf("%s: Open = %x, %v; want ErrDecrypt", name, got, err)
		}
	}
}

func TestParseKeyRequiresExactly32Bytes(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if k, err := totp.ParseKey(good); err != nil || len(k) != 32 {
		t.Fatalf("ParseKey(32 bytes) = %d bytes, %v", len(k), err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 31)),
		base64.StdEncoding.EncodeToString(make([]byte, 33))} {
		if _, err := totp.ParseKey(bad); !errors.Is(err, totp.ErrInvalidKey) {
			t.Errorf("ParseKey(%q) err = %v, want ErrInvalidKey", bad, err)
		}
	}
}
