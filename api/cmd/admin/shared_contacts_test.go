package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func runAdminWithEnv(appDSN, migrateDSN, stdin string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	getenv := func(k string) string {
		switch k {
		case "DATABASE_URL":
			return appDSN
		case "MIGRATE_DATABASE_URL":
			return migrateDSN
		default:
			return ""
		}
	}
	code = run(context.Background(), args, strings.NewReader(stdin), &out, &errOut, getenv)
	return code, out.String(), errOut.String()
}

func seedSharedContactForCLI(t *testing.T, ctx context.Context, schoolName, contactName, headingName, zone, notes, status string) (sharedID, contactID, orgID string) {
	t.Helper()
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO organizations (kind, name) VALUES ('school', $1) RETURNING id::text`, schoolName).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var headingID string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, $2) RETURNING id::text`, orgID, headingName).Scan(&headingID); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO contacts (tenant_id, heading_id, name, zone, notes) VALUES ($1, $2, $3, $4, $5) RETURNING id::text`,
		orgID, headingID, contactName, zone, notes).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name, zone) VALUES ($1, $2, $3, $4) RETURNING id::text`,
		status, headingName, contactName, zone).Scan(&sharedID); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id) VALUES ($1, $2, $3)`,
		orgID, contactID, sharedID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id = $1`, sharedID)
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM contacts WHERE id = $1`, contactID)
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM contact_headings WHERE id = $1`, headingID)
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	})
	return sharedID, contactID, orgID
}

func sharedStatus(t *testing.T, ctx context.Context, id string) string {
	t.Helper()
	var status string
	if err := tdb.Super.QueryRow(ctx, `SELECT status FROM shared_contacts WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestSharedContactsCLIPendingApproveRejectRemove(t *testing.T) {
	ctx := integration(t)
	pendingID, _, _ := seedSharedContactForCLI(t, ctx, "Escuela Privada Origen", "Gazebos Ficticios SRL", "gazebos", "Yerba Buena", "Pagan tarde, pedir seña", "pending")
	approvedID, _, _ := seedSharedContactForCLI(t, ctx, "Otra Escuela", "Sonido Ficticio", "sonido e iluminación", "San Miguel de Tucumán", "Nota secreta", "approved")
	rejectedID, _, _ := seedSharedContactForCLI(t, ctx, "Tercera Escuela", "Catering Rechazado", "foodtrucks", "Tafí Viejo", "Otra nota", "rejected")

	// pending lists id, name, heading, zone, date — never school name or notes.
	code, stdout, stderr := runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "pending")
	if code != 0 || stderr != "" {
		t.Fatalf("pending: exit=%d stderr=%q want 0", code, stderr)
	}
	if !strings.Contains(stdout, pendingID) ||
		!strings.Contains(stdout, "Gazebos Ficticios SRL") ||
		!strings.Contains(stdout, "gazebos") ||
		!strings.Contains(stdout, "Yerba Buena") ||
		!strings.Contains(stdout, time.Now().UTC().Format("2006-01-02")) {
		t.Fatalf("pending stdout = %q, missing expected fields", stdout)
	}
	if strings.Contains(stdout, "Escuela Privada Origen") || strings.Contains(stdout, "Pagan tarde") || strings.Contains(stdout, approvedID) || strings.Contains(stdout, rejectedID) {
		t.Fatalf("pending leaked school, notes, or non-pending item: %q", stdout)
	}

	// approve pending -> approved, and repeating approve is idempotent.
	code, stdout, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "approve", pendingID)
	if code != 0 || stderr != "" || !strings.Contains(stdout, pendingID) {
		t.Fatalf("approve: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := sharedStatus(t, ctx, pendingID); got != "approved" {
		t.Fatalf("status after approve = %q, want approved", got)
	}
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "approve", pendingID)
	if code != 0 || stderr != "" {
		t.Fatalf("idempotent approve: exit=%d stderr=%q", code, stderr)
	}

	// remove approved -> removed, and repeating remove is idempotent.
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "remove", pendingID)
	if code != 0 || stderr != "" {
		t.Fatalf("remove: exit=%d stderr=%q", code, stderr)
	}
	if got := sharedStatus(t, ctx, pendingID); got != "removed" {
		t.Fatalf("status after remove = %q, want removed", got)
	}
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "remove", pendingID)
	if code != 0 || stderr != "" {
		t.Fatalf("idempotent remove: exit=%d stderr=%q", code, stderr)
	}

	// Negative cases for approve: rejected, removed, non-existent.
	for _, tc := range []struct {
		name string
		id   string
		want string
	}{
		{"rejected", rejectedID, "shared contact " + rejectedID + " is not pending"},
		{"removed", pendingID, "shared contact " + pendingID + " is not pending"},
		{"missing", "00000000-0000-0000-0000-000000000000", "shared contact 00000000-0000-0000-0000-000000000000 not found"},
		{"malformed", "not-a-uuid", "shared contact not-a-uuid not found"},
	} {
		t.Run("approve/"+tc.name, func(t *testing.T) {
			code, stdout, stderr := runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "approve", tc.id)
			if code == 0 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want non-zero and %q", code, stdout, stderr, tc.want)
			}
		})
	}
	if got := sharedStatus(t, ctx, rejectedID); got != "rejected" {
		t.Fatalf("rejected status changed to %q", got)
	}
	if got := sharedStatus(t, ctx, pendingID); got != "removed" {
		t.Fatalf("removed status changed to %q", got)
	}

	// reject pending -> rejected, and idempotent repeat.
	pending2, _, _ := seedSharedContactForCLI(t, ctx, "Cuarta Escuela", "Carpa Pendiente", "gazebos", "Concepción", "Secret", "pending")
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "reject", pending2)
	if code != 0 || stderr != "" || sharedStatus(t, ctx, pending2) != "rejected" {
		t.Fatalf("reject: exit=%d stderr=%q status=%q", code, stderr, sharedStatus(t, ctx, pending2))
	}
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "reject", pending2)
	if code != 0 || stderr != "" {
		t.Fatalf("idempotent reject: exit=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "reject", approvedID)
	if code == 0 || !strings.Contains(stderr, "shared contact "+approvedID+" is not pending") {
		t.Fatalf("reject approved: exit=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "remove", rejectedID)
	if code == 0 || !strings.Contains(stderr, "shared contact "+rejectedID+" is not approved") {
		t.Fatalf("remove rejected: exit=%d stderr=%q", code, stderr)
	}
}

func TestSharedContactsCLIRefusesAppRoleAndUsageErrors(t *testing.T) {
	ctx := integration(t)
	pendingID, _, _ := seedSharedContactForCLI(t, ctx, "Escuela AppRole", "Contacto Pendiente", "gazebos", "Yerba Buena", "", "pending")

	code, stdout, stderr := runAdminWithEnv(tdb.AppDSN, tdb.AppDSN, "", "shared-contacts", "pending")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "MIGRATE_DATABASE_URL") {
		t.Fatalf("app role pending: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runAdminWithEnv(tdb.AppDSN, tdb.AppDSN, "", "shared-contacts", "approve", pendingID)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "MIGRATE_DATABASE_URL") {
		t.Fatalf("app role approve: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := sharedStatus(t, ctx, pendingID); got != "pending" {
		t.Fatalf("status changed to %q", got)
	}

	for _, args := range [][]string{
		{"shared-contacts"},
		{"shared-contacts", "unknown"},
		{"shared-contacts", "pending", "extra"},
		{"shared-contacts", "approve"},
		{"shared-contacts", "reject", pendingID, "extra"},
		{"shared-contacts", "remove"},
	} {
		code, stdout, stderr := runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "usage: admin") {
			t.Fatalf("%v: exit=%d stdout=%q stderr=%q want 2 and usage", args, code, stdout, stderr)
		}
	}
}

func TestSharedContactsCLIApproveReplacesPriorVersion(t *testing.T) {
	ctx := integration(t)
	v1ID, contactID, orgID := seedSharedContactForCLI(t, ctx, "Escuela Reemplazo", "Proveedor Viejo", "gazebos", "Yerba Buena", "Privada", "approved")

	var v2ID, withdrawnID string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name, zone) VALUES ('pending', 'gazebos', 'Proveedor Nuevo', 'Tafí Viejo') RETURNING id::text`).Scan(&v2ID); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name, zone) VALUES ('withdrawn', 'gazebos', 'Proveedor Retirado', 'Yerba Buena') RETURNING id::text`).Scan(&withdrawnID); err != nil {
		t.Fatal(err)
	}
	for _, scID := range []string{v2ID, withdrawnID} {
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id) VALUES ($1, $2, $3)`, orgID, contactID, scID); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id IN ($1, $2)`, v2ID, withdrawnID)
	})

	// pending excludes withdrawn entries.
	code, stdout, stderr := runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "pending")
	if code != 0 || stderr != "" {
		t.Fatalf("pending: exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, v2ID) || strings.Contains(stdout, withdrawnID) || strings.Contains(stdout, v1ID) {
		t.Fatalf("pending stdout = %q", stdout)
	}

	// Approving v2 moves v1 to removed and sets v2 to approved.
	code, stdout, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "approve", v2ID)
	if code != 0 || stderr != "" || !strings.Contains(stdout, v2ID) {
		t.Fatalf("approve v2: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := sharedStatus(t, ctx, v2ID); got != "approved" {
		t.Fatalf("v2 status = %q, want approved", got)
	}
	if got := sharedStatus(t, ctx, v1ID); got != "removed" {
		t.Fatalf("v1 status = %q, want removed", got)
	}

	// Approving a withdrawn entry fails and leaves it withdrawn.
	code, _, stderr = runAdminWithEnv(tdb.AppDSN, tdb.SuperDSN, "", "shared-contacts", "approve", withdrawnID)
	if code == 0 || !strings.Contains(stderr, "is not pending") {
		t.Fatalf("approve withdrawn: exit=%d stderr=%q", code, stderr)
	}
	if got := sharedStatus(t, ctx, withdrawnID); got != "withdrawn" {
		t.Fatalf("withdrawn status = %q, want withdrawn", got)
	}
}
