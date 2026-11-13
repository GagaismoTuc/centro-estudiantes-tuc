// Package roster imports a school's roster (padrón) and issues its single-use access codes.
package roster

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// MinKeyLen is the minimum length, in bytes, of the access-code HMAC key.
const MinKeyLen = 32

// crockford is Crockford's base32 alphabet: no I, L, O or U, so codes survive being read aloud or
// copied by hand.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const codeLen = 12 // 60 bits

// ErrInvalidKey means the key is not base64 or is shorter than MinKeyLen bytes.
var ErrInvalidKey = errors.New("roster: access code key must be base64 of at least 32 bytes")

// ParseKey decodes the base64 HMAC key for access codes.
func ParseKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(key) < MinKeyLen {
		return nil, ErrInvalidKey
	}
	return key, nil
}

// NewCode returns a random code in display form, XXXX-XXXX-XXXX.
func NewCode() (string, error) {
	raw := make([]byte, codeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, v := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(crockford[v&31]) // 256 is a multiple of 32, so this is uniform
	}
	return b.String(), nil
}

// NormalizeCode turns what a person typed into the stored form: uppercase, without spaces or
// hyphens, with the look-alikes O→0 and I/L→1.
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-', '\t':
		case 'O':
			b.WriteByte('0')
		case 'I', 'L':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// HashCode returns HMAC-SHA256(key, NormalizeCode(code)), the only form of a code that is stored.
func HashCode(key []byte, code string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(NormalizeCode(code)))
	return m.Sum(nil)
}
