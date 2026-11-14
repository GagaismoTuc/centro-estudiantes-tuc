// Package totp implements time-based one-time passwords (RFC 6238 over the HOTP of RFC 4226:
// HMAC-SHA1, 6 digits, 30-second steps) and the encryption of their secrets at rest. The
// cryptographic primitives (HMAC-SHA1, AES-GCM, crypto/rand) come from the standard library.
package totp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// SecretLen is the length in bytes of a new secret (160 bits, RFC 4226 recommendation).
	SecretLen = 20
	// Digits is the length of a code.
	Digits = 6
	// Step is the time step of RFC 6238.
	Step = 30 * time.Second
	// Issuer names the account in authenticator apps.
	Issuer = "Centro de Estudiantes"
	// KeyLen is the length in bytes of the AES-256 key that encrypts secrets (TOTP_ENC_KEY).
	KeyLen = 32
)

var (
	// ErrInvalidKey means TOTP_ENC_KEY is not base64 of exactly 32 bytes.
	ErrInvalidKey = errors.New("totp: key must be base64 of exactly 32 bytes")
	// ErrDecrypt covers a wrong key, a tampered or truncated ciphertext and a ciphertext of another user.
	ErrDecrypt = errors.New("totp: cannot decrypt secret")
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns SecretLen random bytes.
func NewSecret() ([]byte, error) {
	s := make([]byte, SecretLen)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("totp: secret: %w", err)
	}
	return s, nil
}

// EncodeSecret returns the secret as authenticator apps expect it: base32 without padding.
func EncodeSecret(secret []byte) string { return b32.EncodeToString(secret) }

// URI returns the otpauth:// URI (Key Uri Format) for the account, usually shown as a QR code.
func URI(account string, secret []byte) string {
	issuer := url.PathEscape(Issuer)
	return "otpauth://totp/" + issuer + ":" + url.PathEscape(account) +
		"?secret=" + EncodeSecret(secret) + "&issuer=" + issuer + "&algorithm=SHA1&digits=6&period=30"
}

// StepAt returns the RFC 6238 counter for t: floor(unix seconds / 30).
func StepAt(t time.Time) int64 { return t.Unix() / int64(Step/time.Second) }

// Code returns the 6-digit HOTP (RFC 4226) of secret for counter step.
func Code(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1_000_000)
}

// Verify checks code against the steps now-1, now and now+1 (one step of clock drift each way) and
// returns the matching step. A step at or before lastUsed is rejected, so a code is accepted at most
// once; pass -1 when no code was used yet. Codes are compared in constant time.
func Verify(secret []byte, code string, now time.Time, lastUsed int64) (int64, bool) {
	if len(code) != Digits || strings.Trim(code, "0123456789") != "" {
		return 0, false
	}
	cur := StepAt(now)
	found, ok := int64(0), false
	for step := cur - 1; step <= cur+1; step++ {
		match := subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(code)) == 1
		if match && step > lastUsed && !ok {
			found, ok = step, true
		}
	}
	return found, ok
}

// ParseKey decodes TOTP_ENC_KEY: standard base64 of exactly KeyLen bytes.
func ParseKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(key) != KeyLen {
		return nil, ErrInvalidKey
	}
	return key, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts secret with AES-256-GCM under key and a random 12-byte nonce, binding it to userID
// as additional data so a ciphertext copied to another user's row does not decrypt. The result is
// nonce || ciphertext || tag.
func Seal(key []byte, userID string, secret []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, fmt.Errorf("totp: seal: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("totp: nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, secret, []byte(userID)), nil
}

// Open reverses Seal; any mismatch returns ErrDecrypt.
func Open(key []byte, userID string, sealed []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrDecrypt
	}
	n := aead.NonceSize()
	secret, err := aead.Open(nil, sealed[:n], sealed[n:], []byte(userID))
	if err != nil {
		return nil, ErrDecrypt
	}
	return secret, nil
}
