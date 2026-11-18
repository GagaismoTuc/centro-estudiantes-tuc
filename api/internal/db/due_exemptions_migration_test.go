package db_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

func TestDueExemptionsMigration00031(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00031_due_exemptions.sql"); err != nil {
		t.Fatalf("due exemptions migration 00031 is missing: %v", err)
	}
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	defer stop()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", env.SuperDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}

	// The table exists with RLS enabled and forced.
	var rls, forced bool
	if err = env.Super.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = 'public.finance_due_exemptions'::regclass`).Scan(&rls, &forced); err != nil {
		t.Fatalf("finance_due_exemptions missing: %v", err)
	}
	if !rls || !forced {
		t.Fatalf("RLS enabled=%v forced=%v, want both true", rls, forced)
	}

	// ce_app may only read and insert: an Exemption is never edited or removed.
	for priv, want := range map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": false, "DELETE": false} {
		var has bool
		if err = env.Super.QueryRow(ctx, `SELECT has_table_privilege('ce_app', 'public.finance_due_exemptions', $1)`, priv).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has != want {
			t.Fatalf("ce_app %s on finance_due_exemptions = %v, want %v", priv, has, want)
		}
	}

	// The member's snapshot row can be locked FOR UPDATE (shared with #78's payments).
	lockGrant := func() (hasGrant, payments bool) {
		t.Helper()
		if err := env.Super.QueryRow(ctx, `SELECT has_column_privilege('ce_app', 'public.finance_due_members', 'roster_member_id', 'UPDATE'), to_regclass('public.finance_due_payments') IS NOT NULL`).Scan(&hasGrant, &payments); err != nil {
			t.Fatal(err)
		}
		return
	}
	if has, _ := lockGrant(); !has {
		t.Fatal("ce_app lacks UPDATE (roster_member_id) on finance_due_members")
	}

	// The composite FK to roster_members has a supporting index (the PK starts with due_id).
	rosterIndex := func() string {
		t.Helper()
		var def string
		if err := env.Super.QueryRow(ctx, `SELECT coalesce((SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'finance_due_exemptions_roster_idx'), '')`).Scan(&def); err != nil {
			t.Fatal(err)
		}
		return def
	}
	if def := rosterIndex(); !strings.HasSuffix(def, "ON public.finance_due_exemptions USING btree (tenant_id, roster_member_id)") {
		t.Fatalf("finance_due_exemptions_roster_idx = %q, want btree (tenant_id, roster_member_id)", def)
	}

	var schoolA, schoolB, userA, owedA, notOwedA, rosterB string
	if err = env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela A') RETURNING id::text`).Scan(&schoolA); err != nil {
		t.Fatal(err)
	}
	if err = env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela B') RETURNING id::text`).Scan(&schoolB); err != nil {
		t.Fatal(err)
	}
	if err = env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash,email_verified_at) VALUES ('ana@a.example','hash',now()) RETURNING id::text`).Scan(&userA); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		tenant, name, doc string
		dst               *string
	}{
		{schoolA, "Ana Ficticia", "30111222", &owedA},
		{schoolA, "Carla Ficticia", "30111224", &notOwedA},
		{schoolB, "Beto Ficticio", "30111223", &rosterB},
	} {
		if err = env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,$2,$3) RETURNING id::text`, r.tenant, r.name, r.doc).Scan(r.dst); err != nil {
			t.Fatal(err)
		}
	}
	var dueA, dueB string
	if err = env.Super.QueryRow(ctx, `INSERT INTO finance_dues(tenant_id,name,amount_cents,due_date,created_by) VALUES($1,'Cuota de noviembre',200000,'2026-11-30',$2) RETURNING id::text`, schoolA, userA).Scan(&dueA); err != nil {
		t.Fatal(err)
	}
	if _, err = env.Super.Exec(ctx, `INSERT INTO finance_due_members(tenant_id,due_id,roster_member_id) VALUES($1,$2,$3)`, schoolA, dueA, owedA); err != nil {
		t.Fatal(err)
	}
	if err = env.Super.QueryRow(ctx, `INSERT INTO finance_dues(tenant_id,name,amount_cents,due_date,created_by) VALUES($1,'Cuota B',100000,'2026-11-30',$2) RETURNING id::text`, schoolB, userA).Scan(&dueB); err != nil {
		t.Fatal(err)
	}
	if _, err = env.Super.Exec(ctx, `INSERT INTO finance_due_members(tenant_id,due_id,roster_member_id) VALUES($1,$2,$3)`, schoolB, dueB, rosterB); err != nil {
		t.Fatal(err)
	}

	insert := func(asTenant, tenant, due, member, reason string) error {
		return db.WithTenant(ctx, env.App, asTenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO finance_due_exemptions(tenant_id,due_id,roster_member_id,reason,created_by) VALUES($1,$2,$3,$4,$5)`, tenant, due, member, reason, userA)
			return err
		})
	}
	if err = insert(schoolA, schoolA, dueA, owedA, strings.Repeat("á", 500)); err != nil {
		t.Fatalf("insert exemption as ce_app: %v", err)
	}

	var pe *pgconn.PgError
	for _, tc := range []struct {
		name, tenantCtx, tenant, due, member, reason, code string
	}{
		{"duplicate", schoolA, schoolA, dueA, owedA, "Beca", "23505"},
		{"blank reason", schoolA, schoolA, dueA, notOwedA, "   ", "23514"},
		{"reason over 500", schoolA, schoolA, dueA, notOwedA, strings.Repeat("a", 501), "23514"},
		{"member not owed", schoolA, schoolA, dueA, notOwedA, "Beca", "23503"},
		{"cross-tenant member", schoolA, schoolA, dueA, rosterB, "Beca", "23503"},
		{"cross-tenant due", schoolA, schoolA, dueB, owedA, "Beca", "23503"},
		{"other tenant row", schoolA, schoolB, dueB, rosterB, "Beca", "42501"},
	} {
		err = insert(tc.tenantCtx, tc.tenant, tc.due, tc.member, tc.reason)
		if !errors.As(err, &pe) || pe.Code != tc.code {
			t.Fatalf("%s: got %v, want %s", tc.name, err, tc.code)
		}
	}

	// ce_app cannot change or remove an Exemption.
	for _, stmt := range []string{
		`UPDATE finance_due_exemptions SET reason = 'Otra'`,
		`DELETE FROM finance_due_exemptions`,
	} {
		err = db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Fatalf("%q as ce_app: got %v, want 42501", stmt, err)
		}
	}

	// RLS: tenant B sees none of tenant A's Exemptions.
	if err = db.WithTenant(ctx, env.App, schoolB, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM finance_due_exemptions`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("RLS leaked %d exemptions to schoolB", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err = env.Super.QueryRow(ctx, `SELECT count(*) FROM finance_due_exemptions`).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored exemptions = %d (err=%v), want 1", stored, err)
	}

	// Down/Up pinned between the latest earlier migration and 00031.
	var prevVersion int64
	for _, src := range p.ListSources() {
		if src.Version < 31 && src.Version > prevVersion {
			prevVersion = src.Version
		}
	}
	if prevVersion == 0 {
		t.Fatal("no migration found before 00031")
	}
	if _, err = p.DownTo(ctx, prevVersion); err != nil {
		t.Fatalf("down to %d: %v", prevVersion, err)
	}
	var exemptions, members bool
	if err = env.Super.QueryRow(ctx, `SELECT to_regclass('public.finance_due_exemptions') IS NOT NULL, to_regclass('public.finance_due_members') IS NOT NULL`).Scan(&exemptions, &members); err != nil {
		t.Fatal(err)
	}
	if exemptions || !members {
		t.Fatalf("after down: exemptions=%v members=%v, want false/true", exemptions, members)
	}
	if has, payments := lockGrant(); has != payments {
		t.Fatalf("after down: lock grant=%v, want it only while finance_due_payments exists (%v)", has, payments)
	}
	if _, err = p.UpTo(ctx, 31); err != nil {
		t.Fatalf("reapply 00031: %v", err)
	}
	if err = env.Super.QueryRow(ctx, `SELECT to_regclass('public.finance_due_exemptions') IS NOT NULL`).Scan(&exemptions); err != nil || !exemptions {
		t.Fatalf("after up: exemptions=%v err=%v", exemptions, err)
	}
	if def := rosterIndex(); !strings.HasSuffix(def, "ON public.finance_due_exemptions USING btree (tenant_id, roster_member_id)") {
		t.Fatalf("after up: finance_due_exemptions_roster_idx = %q", def)
	}
}
