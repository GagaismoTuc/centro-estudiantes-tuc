package main

import (
	"encoding/base64"
	"testing"
)

func TestRunRefusesToStartWithoutValidAccessCodeKey(t *testing.T) {
	const want = "ACCESS_CODE_KEY must be base64 of at least 32 random bytes (generate it with: openssl rand -base64 32)"
	for _, tc := range []struct{ name, key string }{
		{"missing", ""},
		{"not base64", "not base64!"},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Unreachable databases: the key must be rejected before any connection is attempted.
			env := map[string]string{
				"DATABASE_URL":         "postgres://nobody@127.0.0.1:1/none",
				"MIGRATE_DATABASE_URL": "postgres://nobody@127.0.0.1:1/none",
				"ACCESS_CODE_KEY":      tc.key,
			}
			err := run(func(k string) string { return env[k] })
			if err == nil || err.Error() != want {
				t.Fatalf("run() = %v, want %q", err, want)
			}
		})
	}
}
