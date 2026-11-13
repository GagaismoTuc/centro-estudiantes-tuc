// Package auth holds password hashing, server-side sessions and the sign-in rate limiter.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: the OWASP Password Storage Cheat Sheet configuration m=19 MiB, t=2, p=1.
// Low memory keeps concurrent sign-ins affordable on a small school server. The parameters are
// stored in every hash, so raising them later only affects new hashes.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	saltLen        = 16
	keyLen         = 32
)

var b64 = base64.RawStdEncoding

// ErrMalformedHash is returned by VerifyPassword when the stored hash is not a PHC argon2id string.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// HashPassword returns an argon2id hash in PHC string format:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<key> (unpadded standard base64).
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC hash, comparing in constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, ErrMalformedHash
	}
	var m, t uint32
	var p uint8
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || n != 3 || m == 0 || t == 0 || p == 0 {
		return false, ErrMalformedHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, ErrMalformedHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified when the email is unknown, so a miss costs the same time as a wrong password.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("dummy password for timing equalisation")
	if err != nil {
		panic(err)
	}
	return h
})
