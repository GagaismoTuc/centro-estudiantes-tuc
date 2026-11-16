package contacts_test

import (
	"context"
	"errors"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/contacts"
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type storeFixture struct {
	tenant string
	store  *contacts.Store
	now    time.Time
}

func newFixture(t *testing.T, ctx context.Context) *storeFixture {
	t.Helper()
	now := time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC)
	var tenant string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO organizations (kind, name) VALUES ('school', 'Escuela Test') RETURNING id::text`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `
		INSERT INTO contact_headings (tenant_id, name)
		SELECT $1, name FROM unnest($2::text[]) AS name`, tenant, auth.DefaultContactHeadings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, tenant)
	})
	return &storeFixture{
		tenant: tenant,
		store:  contacts.NewStore(tdb.App, func() time.Time { return now }),
		now:    now,
	}
}

func (f *storeFixture) seedMember(t *testing.T, ctx context.Context, email, role string, withOfficerPosition, mfaVerified bool) auth.Session {
	t.Helper()
	var userID string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`, email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var rosterID *string
	if withOfficerPosition {
		var rid string
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, $2, $3) RETURNING id::text`, f.tenant, email, email).Scan(&rid); err != nil {
			t.Fatal(err)
		}
		rosterID = &rid
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Tesorero')`, f.tenant, rid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role, roster_member_id) VALUES ($1, $2, $3, $4)`, f.tenant, userID, role, rosterID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM officer_positions WHERE tenant_id = $1`, f.tenant)
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM memberships WHERE user_id = $1`, userID)
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM roster_members WHERE tenant_id = $1`, f.tenant)
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})
	return auth.Session{
		UserID:      userID,
		Email:       email,
		TenantID:    f.tenant,
		MFAVerified: mfaVerified,
	}
}

func TestContactsStoreRolesAndMFACheck(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)

	officerMFA := f.seedMember(t, ctx, "officer-mfa@example.com", "student", true, true)
	officerNoMFA := f.seedMember(t, ctx, "officer-nomfa@example.com", "officer", false, false)
	adminMFA := f.seedMember(t, ctx, "admin-mfa@example.com", "school_admin", false, true)
	adminNoMFA := f.seedMember(t, ctx, "admin-nomfa@example.com", "school_admin", false, false)
	student := f.seedMember(t, ctx, "student@example.com", "student", false, false)

	// Officer via officer_positions + MFA can read and write.
	headings, err := f.store.ListHeadings(ctx, officerMFA)
	if err != nil || len(headings) != len(auth.DefaultContactHeadings) {
		t.Fatalf("officerMFA ListHeadings = %d, err=%v", len(headings), err)
	}
	created, err := f.store.CreateHeading(ctx, officerMFA, "Fotografía")
	if err != nil || created.Name != "Fotografía" {
		t.Fatalf("officerMFA CreateHeading = %+v, err=%v", created, err)
	}

	// School admin + MFA can read, cannot write.
	if _, err := f.store.ListContacts(ctx, adminMFA, "", ""); err != nil {
		t.Fatalf("adminMFA ListContacts = %v", err)
	}
	if _, err := f.store.CreateHeading(ctx, adminMFA, "Blocked"); !errors.Is(err, contacts.ErrForbidden) {
		t.Fatalf("adminMFA CreateHeading = %v, want ErrForbidden", err)
	}

	// School admin without MFA gets ErrMFARequired on read, ErrForbidden on write.
	if _, err := f.store.ListContacts(ctx, adminNoMFA, "", ""); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("adminNoMFA ListContacts = %v, want ErrMFARequired", err)
	}
	if _, err := f.store.CreateHeading(ctx, adminNoMFA, "Blocked"); !errors.Is(err, contacts.ErrForbidden) {
		t.Fatalf("adminNoMFA CreateHeading = %v, want ErrForbidden", err)
	}

	// Officer without MFA gets ErrMFARequired on read and write.
	if _, err := f.store.ListContacts(ctx, officerNoMFA, "", ""); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("officerNoMFA ListContacts = %v, want ErrMFARequired", err)
	}
	if _, err := f.store.CreateHeading(ctx, officerNoMFA, "Blocked"); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("officerNoMFA CreateHeading = %v, want ErrForbidden/ErrMFARequired", err)
	}

	// Student gets ErrForbidden on read and write.
	if _, err := f.store.ListContacts(ctx, student, "", ""); !errors.Is(err, contacts.ErrForbidden) {
		t.Fatalf("student ListContacts = %v, want ErrForbidden", err)
	}
	if _, err := f.store.CreateHeading(ctx, student, "Blocked"); !errors.Is(err, contacts.ErrForbidden) {
		t.Fatalf("student CreateHeading = %v, want ErrForbidden", err)
	}
}
