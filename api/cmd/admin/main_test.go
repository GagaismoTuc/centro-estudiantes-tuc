package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

var (
	tdb      *testdb.Env
	setupErr error
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	var stop func()
	tdb, stop, setupErr = testdb.Start(context.Background())
	code := m.Run()
	stop()
	os.Exit(code)
}

func integration(t *testing.T) context.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker; skipped with -short")
	}
	if setupErr != nil {
		t.Fatalf("setup: %v", setupErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

const adminPassword = "clave del administrador"

func runCLI(stdin string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	getenv := func(k string) string {
		if k == "DATABASE_URL" {
			return tdb.AppDSN
		}
		return ""
	}
	code = run(context.Background(), args, strings.NewReader(stdin), &out, &errOut, getenv)
	return code, out.String(), errOut.String()
}

func counts(t *testing.T, ctx context.Context) (orgs, users, memberships int) {
	t.Helper()
	if err := tdb.Super.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM organizations), (SELECT count(*) FROM users), (SELECT count(*) FROM memberships)`).
		Scan(&orgs, &users, &memberships); err != nil {
		t.Fatal(err)
	}
	return
}

func TestBootstrapCreatesSchoolAdminAndRefusesExistingEmail(t *testing.T) {
	ctx := integration(t)
	code, stdout, stderr := runCLI(adminPassword+"\r\n",
		"bootstrap", "--org-name", "Escuela Normal", "--email", "Directora@Example.com")
	if code != 0 {
		t.Fatalf("exit = %d, stderr %q; want 0", code, stderr)
	}

	var orgID, kind, name, userID, email, hash, role string
	var roster *string
	if err := tdb.Super.QueryRow(ctx, `
		SELECT o.id::text, o.kind, o.name, u.id::text, u.email, u.password_hash, m.role, m.roster_member_id::text
		FROM memberships m JOIN organizations o ON o.id = m.tenant_id JOIN users u ON u.id = m.user_id
		WHERE o.name = 'Escuela Normal'`).Scan(&orgID, &kind, &name, &userID, &email, &hash, &role, &roster); err != nil {
		t.Fatalf("read bootstrap rows: %v", err)
	}
	if kind != "school" || email != "directora@example.com" || role != "school_admin" || roster != nil {
		t.Fatalf("got kind=%s email=%s role=%s roster=%v; want school, directora@example.com, school_admin, nil", kind, email, role, roster)
	}
	if ok, err := auth.VerifyPassword(hash, adminPassword); err != nil || !ok {
		t.Fatalf("stored hash does not verify the stdin password: %v %v", ok, err)
	}
	if want := "created organization " + orgID + " with school admin directora@example.com\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "Password: " {
		t.Fatalf("stderr = %q, want the password prompt only", stderr)
	}

	orgs, users, members := counts(t, ctx)
	code, stdout, stderr = runCLI(adminPassword+"\n",
		"bootstrap", "--org-name", "Otra Escuela", "--email", "directora@EXAMPLE.com")
	if code != 1 || stdout != "" || stderr != "Password: admin: email already registered\n" {
		t.Fatalf("existing email: exit=%d stdout=%q stderr=%q; want 1, empty, email already registered", code, stdout, stderr)
	}
	if o, u, m := counts(t, ctx); o != orgs || u != users || m != members {
		t.Fatalf("counts changed to %d/%d/%d, want %d/%d/%d", o, u, m, orgs, users, members)
	}
}

func TestBootstrapUsageErrorsCreateNothing(t *testing.T) {
	ctx := integration(t)
	orgs, users, members := counts(t, ctx)
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"no command", adminPassword, nil, "usage: admin bootstrap --org-name NAME --email EMAIL (password on stdin)\n"},
		{"unknown command", adminPassword, []string{"nuke"}, "usage: admin bootstrap --org-name NAME --email EMAIL (password on stdin)\n"},
		{"password flag is not accepted", adminPassword, []string{"bootstrap", "--org-name", "X", "--email", "x@example.com", "--password", "y"}, "flag provided but not defined: -password"},
		{"missing email", adminPassword, []string{"bootstrap", "--org-name", "X"}, "usage: admin bootstrap --org-name NAME --email EMAIL (password on stdin)\n"},
		{"short password", "corta\n", []string{"bootstrap", "--org-name", "X", "--email", "x@example.com"}, "Password: admin: password must have at least 10 characters\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.stdin, tc.args...)
			if code != 2 || stdout != "" || !strings.HasPrefix(stderr, tc.want) {
				t.Fatalf("exit=%d stdout=%q stderr=%q; want 2, empty, prefix %q", code, stdout, stderr, tc.want)
			}
		})
	}
	if o, u, m := counts(t, ctx); o != orgs || u != users || m != members {
		t.Fatalf("counts changed to %d/%d/%d, want %d/%d/%d", o, u, m, orgs, users, members)
	}
}

// seedMFAUser creates a user with a confirmed TOTP, two recovery codes and two sessions.
func seedMFAUser(t *testing.T, ctx context.Context, email string) string {
	t.Helper()
	var id string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO user_totp (user_id, secret_enc, confirmed_at, last_used_step)
		VALUES ($1, $2, $3, 1)`, id, make([]byte, 48), now); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		code := make([]byte, 32)
		_, _ = rand.Read(code)
		tok := make([]byte, 32)
		_, _ = rand.Read(tok)
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2)`, id, code); err != nil {
			t.Fatal(err)
		}
		var mfaAt *time.Time // one two-factor session, one pending
		if i == 0 {
			mfaAt = &now
		}
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, created_at, expires_at, mfa_at)
			VALUES ($1, $2, $3, $4, $5)`, tok, id, now, now.Add(time.Hour), mfaAt); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// mfaRows counts a user's user_totp, recovery_codes and sessions rows.
func mfaRows(t *testing.T, ctx context.Context, user string) [3]int {
	t.Helper()
	var n [3]int
	if err := tdb.Super.QueryRow(ctx, `SELECT (SELECT count(*) FROM user_totp WHERE user_id = $1),
		(SELECT count(*) FROM recovery_codes WHERE user_id = $1), (SELECT count(*) FROM sessions WHERE user_id = $1)`,
		user).Scan(&n[0], &n[1], &n[2]); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestResetMFADeletesTOTPRecoveryCodesAndSessions(t *testing.T) {
	ctx := integration(t)
	suffix := strings.ToLower(rand.Text())
	lost := seedMFAUser(t, ctx, "perdio-"+suffix+"@example.com")
	other := seedMFAUser(t, ctx, "otro-"+suffix+"@example.com")

	code, stdout, stderr := runCLI("", "reset-mfa", "--email", " Perdio-"+strings.ToUpper(suffix)+"@Example.com ")
	if code != 0 || stderr != "" || stdout != "reset two-factor authentication of perdio-"+suffix+"@example.com and signed out its sessions\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if n := mfaRows(t, ctx, lost); n != [3]int{} {
		t.Fatalf("totp/recovery/sessions = %v, want all 0", n)
	}
	if n := mfaRows(t, ctx, other); n != [3]int{1, 2, 2} {
		t.Fatalf("other user's rows = %v, want untouched 1/2/2", n)
	}

	code, stdout, stderr = runCLI("", "reset-mfa", "--email", "nadie-"+suffix+"@example.com")
	if code != 1 || stdout != "" || stderr != "admin: no account with that email\n" {
		t.Fatalf("unknown email: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{{"reset-mfa"}, {"reset-mfa", "--email", "x@example.com", "extra"}, {"reset-mfa", "--email", "sin-arroba"}} {
		if code, stdout, stderr := runCLI("", args...); code != 2 || stdout != "" || !strings.Contains(stderr, "usage: admin") {
			t.Fatalf("%v: exit=%d stdout=%q stderr=%q; want usage error", args, code, stdout, stderr)
		}
	}
	if n := mfaRows(t, ctx, other); n != [3]int{1, 2, 2} {
		t.Fatalf("other user's rows = %v after errors, want 1/2/2", n)
	}
}
