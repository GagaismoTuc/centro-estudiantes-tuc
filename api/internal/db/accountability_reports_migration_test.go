package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

// TestAccountabilityReportsMigration00036 covers R2 and R8 at the database: ce_app may only insert
// and read a Financial accountability report, RLS is forced per school, and the Down drops it.
func TestAccountabilityReportsMigration00036(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00036_accountability_reports.sql"); err != nil {
		t.Fatalf("accountability reports migration 00036 is missing: %v", err)
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

	var schoolA, schoolB, user string
	for _, s := range []struct {
		dst *string
		sql string
	}{
		{&schoolA, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela A') RETURNING id::text`},
		{&schoolB, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela B') RETURNING id::text`},
		{&user, `INSERT INTO users(email,password_hash,email_verified_at) VALUES ('ana@a.example','hash',now()) RETURNING id::text`},
	} {
		if err := env.Super.QueryRow(ctx, s.sql).Scan(s.dst); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(tx pgx.Tx, tenant, term, from, to string) (string, error) {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO finance_accountability_reports(tenant_id,period_from,period_to,term,published_by,published_by_name,report)
			VALUES ($1,$2,$3,$4,$5,'Ana Ficticia','{"openingCents":0}') RETURNING id::text`, tenant, from, to, term, user).Scan(&id)
		return id, err
	}

	var report string
	if err := db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) (e error) {
		report, e = insert(tx, schoolA, "Gestión 2026", "2026-03-01", "2026-11-30")
		return e
	}); err != nil {
		t.Fatalf("insert report as ce_app: %v", err)
	}

	var pe *pgconn.PgError
	wantCode := func(name, code string, err error) {
		t.Helper()
		if !errors.As(err, &pe) || pe.Code != code {
			t.Fatalf("%s: got %v, want %s", name, err, code)
		}
	}

	// R2: published copies never change; ce_app holds only INSERT and SELECT.
	wantCode("update report", "42501", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE finance_accountability_reports SET term='Otra' WHERE id=$1`, report)
		return err
	}))
	wantCode("delete report", "42501", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM finance_accountability_reports WHERE id=$1`, report)
		return err
	}))
	var grants string
	if err := env.Super.QueryRow(ctx, `SELECT string_agg(privilege_type, ',' ORDER BY privilege_type) FROM information_schema.role_table_grants
		WHERE grantee='ce_app' AND table_name='finance_accountability_reports'`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != "INSERT,SELECT" {
		t.Fatalf("ce_app grants=%q, want INSERT,SELECT", grants)
	}

	// The Gestión is required and the period is ordered.
	wantCode("blank term", "23514", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := insert(tx, schoolA, "  ", "2026-03-01", "2026-11-30")
		return err
	}))
	wantCode("reversed period", "23514", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := insert(tx, schoolA, "Gestión 2026", "2026-11-30", "2026-03-01")
		return err
	}))

	// R8: RLS is forced; another school neither sees nor writes school A's reports.
	var forced bool
	if err := env.Super.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname='finance_accountability_reports'`).Scan(&forced); err != nil || !forced {
		t.Fatalf("forced RLS=%v err=%v", forced, err)
	}
	if err := db.WithTenant(ctx, env.App, schoolB, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM finance_accountability_reports`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("school B sees %d reports of school A", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wantCode("cross-tenant insert", "42501", db.WithTenant(ctx, env.App, schoolB, func(tx pgx.Tx) error {
		_, err := insert(tx, schoolA, "Gestión 2026", "2026-03-01", "2026-11-30")
		return err
	}))

	// DownTo/UpTo pin this migration: earlier or later ones must not shift what this test rolls back.
	if _, err := p.DownTo(ctx, 35); err != nil {
		t.Fatalf("down: %v", err)
	}
	var exists bool
	if err := env.Super.QueryRow(ctx, `SELECT to_regclass('finance_accountability_reports') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("table after down exists=%v err=%v", exists, err)
	}
	if _, err := p.UpTo(ctx, 36); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
