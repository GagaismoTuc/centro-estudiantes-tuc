package finance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

func TestStoreDues(t *testing.T) {
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	var emptyTenant, tenant, officerUser, posUser, adminUser, studentUser, anaRoster, betoRoster, carlaRoster string
	if err := env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Vacia') RETURNING id::text`).Scan(&emptyTenant); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela Demo') RETURNING id::text`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct {
		email string
		dst   *string
	}{
		{"ana@example.invalid", &officerUser},
		{"beto@example.invalid", &posUser},
		{"admin@example.invalid", &adminUser},
		{"carla@example.invalid", &studentUser},
	} {
		if err := env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES ($1,'x') RETURNING id::text`, u.email).Scan(u.dst); err != nil {
			t.Fatal(err)
		}
	}

	// Empty roster rejects creation with ErrEmptyRoster and writes nothing.
	if _, err := env.Super.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES ($1,$2,'officer')`, emptyTenant, officerUser); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := finance.NewStore(env.App, func() time.Time { return now })
	if _, err := store.CreateDue(ctx, auth.Session{TenantID: emptyTenant, UserID: officerUser, MFAVerified: true}, finance.NewDue{
		Name: "Cuota de noviembre", AmountCents: 200000, DueDate: "2026-11-30",
	}); !errors.Is(err, finance.ErrEmptyRoster) {
		t.Fatalf("empty roster: got %v, want ErrEmptyRoster", err)
	}
	var count int
	if err := env.Super.QueryRow(ctx, `SELECT count(*) FROM finance_dues WHERE tenant_id=$1`, emptyTenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty roster persisted rows=%d err=%v", count, err)
	}

	// Seed roster members Ana and Beto; Carla is added after creation.
	if err := env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Ana Ficticia','30111222') RETURNING id::text`, tenant).Scan(&anaRoster); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Beto Ficticio','30111223') RETURNING id::text`, tenant).Scan(&betoRoster); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'officer',$3)`, tenant, officerUser, anaRoster); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'student',$3)`, tenant, posUser, betoRoster); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO officer_positions(tenant_id,roster_member_id,position) VALUES ($1,$2,'Tesorero')`, tenant, betoRoster); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES ($1,$2,'school_admin')`, tenant, adminUser); err != nil {
		t.Fatal(err)
	}

	anaSess := auth.Session{TenantID: tenant, UserID: officerUser, MFAVerified: true}
	betoSess := auth.Session{TenantID: tenant, UserID: posUser, MFAVerified: true}
	adminSess := auth.Session{TenantID: tenant, UserID: adminUser, MFAVerified: true}

	// Officer without MFA gets ErrMFARequired.
	if _, err := store.CreateDue(ctx, auth.Session{TenantID: tenant, UserID: officerUser, MFAVerified: false}, finance.NewDue{
		Name: "Cuota", AmountCents: 200000, DueDate: "2026-11-30",
	}); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("unverified MFA: got %v", err)
	}

	created, err := store.CreateDue(ctx, anaSess, finance.NewDue{
		Name: "Cuota de noviembre", AmountCents: 200000, DueDate: "2026-11-30",
	})
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	if created.TotalCount != 2 || created.OwingCount != 2 || len(created.Members) != 2 {
		t.Fatalf("created counts: %+v", created)
	}

	// Import Carla after due creation; Carla does not owe it and is absent from GetDue and ListMyDues.
	if err := env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Carla Ficticia','30111224') RETURNING id::text`, tenant).Scan(&carlaRoster); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'student',$3)`, tenant, studentUser, carlaRoster); err != nil {
		t.Fatal(err)
	}

	carlaSess := auth.Session{TenantID: tenant, UserID: studentUser}
	carlaMine, err := store.ListMyDues(ctx, carlaSess)
	if err != nil || len(carlaMine) != 0 {
		t.Fatalf("carla mine=%+v err=%v", carlaMine, err)
	}
	betoMine, err := store.ListMyDues(ctx, betoSess)
	if err != nil || len(betoMine) != 1 || betoMine[0].Status != finance.DueStatusOwing {
		t.Fatalf("beto mine=%+v err=%v", betoMine, err)
	}
	if _, err := store.ListDues(ctx, carlaSess); !errors.Is(err, finance.ErrForbidden) {
		t.Fatalf("student ListDues: got %v", err)
	}
	if _, err := store.GetDue(ctx, carlaSess, created.ID); !errors.Is(err, finance.ErrForbidden) {
		t.Fatalf("student GetDue: got %v", err)
	}

	// Admin reads full list; position holder updates and deletes.
	adminList, err := store.ListDues(ctx, adminSess)
	if err != nil || len(adminList) != 1 || adminList[0].OwingCount != 2 {
		t.Fatalf("admin list=%+v err=%v", adminList, err)
	}
	newName := "Cuota noviembre actualizada"
	newAmt := int64(250000)
	updated, err := store.UpdateDue(ctx, betoSess, created.ID, finance.UpdateDue{Name: &newName, AmountCents: &newAmt})
	if err != nil || updated.Name != newName || updated.AmountCents != newAmt {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	if err := store.DeleteDue(ctx, betoSess, created.ID); err != nil {
		t.Fatalf("delete due: %v", err)
	}
	if after, err := store.ListDues(ctx, anaSess); err != nil || len(after) != 0 {
		t.Fatalf("after delete=%+v err=%v", after, err)
	}
}
