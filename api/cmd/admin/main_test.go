package main

import (
	"bytes"
	"context"
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
		{"short password", "corta\n", []string{"bootstrap", "--org-name", "X", "--email", "x@example.com"}, "Password: admin: password must have at least 12 characters\n"},
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
