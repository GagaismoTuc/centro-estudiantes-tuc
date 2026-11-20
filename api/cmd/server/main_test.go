package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestServerCommandPreservesMissingDatabaseError(t *testing.T) {
	if os.Getenv("TEST_SERVER_COMMAND_CHILD") == "1" {
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestServerCommandPreservesMissingDatabaseError$")
	cmd.Env = []string{"TEST_SERVER_COMMAND_CHILD=1", "GOTENBERG_URL=http://127.0.0.1:3000", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_URL and MIGRATE_DATABASE_URL must be set") {
		t.Fatalf("command error=%v output=%s", err, out)
	}
}

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

func TestRunRefusesToStartWithoutValidTOTPKey(t *testing.T) {
	const want = "TOTP_ENC_KEY must be base64 of exactly 32 random bytes (generate it with: openssl rand -base64 32)"
	for _, tc := range []struct{ name, key string }{
		{"missing", ""},
		{"not base64", "not base64!"},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16))},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 64))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Mail and base URL are valid and the databases unreachable: the key must be rejected
			// before any connection is attempted.
			env := map[string]string{
				"DATABASE_URL":         "postgres://nobody@127.0.0.1:1/none",
				"MIGRATE_DATABASE_URL": "postgres://nobody@127.0.0.1:1/none",
				"ACCESS_CODE_KEY":      base64.StdEncoding.EncodeToString(make([]byte, 32)),
				"MAIL_DEV_DIR":         t.TempDir(),
				"APP_BASE_URL":         "https://ce.example",
				"TOTP_ENC_KEY":         tc.key,
			}
			err := run(func(k string) string { return env[k] })
			if err == nil || err.Error() != want {
				t.Fatalf("run() = %v, want %q", err, want)
			}
		})
	}
}

func TestRunRefusesToStartWithoutMailConfig(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	const noSender = "set SMTP_HOST (with SMTP_PORT, SMTP_USERNAME, SMTP_PASSWORD, MAIL_FROM) or, for development, MAIL_DEV_DIR"
	const badURL = "APP_BASE_URL must be an https:// origin (http://localhost is allowed for development)"
	const smtpVars = "SMTP_USERNAME, SMTP_PASSWORD and MAIL_FROM must be set with SMTP_HOST"
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no sender", map[string]string{"APP_BASE_URL": "https://ce.example"}, noSender},
		{"smtp without credentials", map[string]string{"APP_BASE_URL": "https://ce.example", "SMTP_HOST": "smtp.example"}, smtpVars},
		{"missing base URL", map[string]string{"MAIL_DEV_DIR": t.TempDir()}, badURL},
		{"plain http", map[string]string{"MAIL_DEV_DIR": t.TempDir(), "APP_BASE_URL": "http://ce.example"}, badURL},
		{"base URL with path", map[string]string{"MAIL_DEV_DIR": t.TempDir(), "APP_BASE_URL": "https://ce.example/app?x=1"}, badURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{
				"DATABASE_URL":         "postgres://nobody@127.0.0.1:1/none",
				"MIGRATE_DATABASE_URL": "postgres://nobody@127.0.0.1:1/none",
				"ACCESS_CODE_KEY":      key,
				"TOTP_ENC_KEY":         key,
			}
			for k, v := range tc.env {
				env[k] = v
			}
			err := run(func(k string) string { return env[k] })
			if err == nil || err.Error() != tc.want {
				t.Fatalf("run() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://ce.example":     "https://ce.example",
		"https://ce.example/":    "https://ce.example",
		"http://localhost:5173":  "http://localhost:5173",
		"http://127.0.0.1:8080/": "http://127.0.0.1:8080",
	} {
		if got, err := parseBaseURL(in); err != nil || got != want {
			t.Errorf("parseBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ce.example", "ftp://ce.example", "https://user@ce.example", "https://ce.example#x", "http://localhost.evil.example"} {
		if _, err := parseBaseURL(in); err == nil {
			t.Errorf("parseBaseURL(%q) = nil error, want error", in)
		}
	}
}
