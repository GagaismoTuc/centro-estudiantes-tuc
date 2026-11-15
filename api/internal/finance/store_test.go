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

func TestStoreEntryAuthorizationAndBalance(t *testing.T) {
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	var tenant, user, member, income, expense string
	if err := env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Fixture') RETURNING id::text`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES ('ana@example.invalid','x') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Ana Ficticia','123') RETURNING id::text`, tenant).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'student',$3)`, tenant, user, member); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO officer_positions(tenant_id,roster_member_id,position) VALUES ($1,$2,'Tesorero')`, tenant, member); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas'`, tenant).Scan(&income); err != nil {
		t.Fatal(err)
	}
	if err := env.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Viajes'`, tenant).Scan(&expense); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	store := finance.NewStore(env.App, func() time.Time { return now })
	sess := auth.Session{TenantID: tenant, UserID: user, MFAVerified: true}
	input := finance.NewEntry{CategoryID: income, Direction: "income", Date: "2026-11-10", AmountCents: 200000, Description: "Venta de tortas"}
	got, err := store.Create(ctx, sess, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.RecordedBy != "Ana Ficticia" || got.Date != input.Date || !got.RecordedAt.Equal(now) {
		t.Fatalf("entry: %+v", got)
	}
	input.CategoryID, input.Direction, input.AmountCents = expense, "expense", 50000
	if _, err := store.Create(ctx, sess, input); err != nil {
		t.Fatal(err)
	}
	balance, err := store.Balance(ctx, sess)
	if err != nil || balance != "150000" {
		t.Fatalf("balance=%s err=%v", balance, err)
	}
	input.AmountCents = 200000
	if _, err := store.Create(ctx, sess, input); err != nil {
		t.Fatal(err)
	}
	balance, err = store.Balance(ctx, sess)
	if err != nil || balance != "-50000" {
		t.Fatalf("negative balance=%s err=%v", balance, err)
	}
	sess.MFAVerified = false
	if _, err := store.Create(ctx, sess, input); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("MFA err=%v", err)
	}
}
