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

func TestDuesMigration00025(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00025_dues.sql"); err != nil {
		t.Fatalf("dues migration 00025 is missing: %v", err)
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

	var schoolA, schoolB, userA, rosterA, rosterB string
	if err = env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela A') RETURNING id::text`).Scan(&schoolA); err != nil {
		t.Fatal(err)
	}
	if err = env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela B') RETURNING id::text`).Scan(&schoolB); err != nil {
		t.Fatal(err)
	}
	if err = env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash,email_verified_at) VALUES ('ana@a.example','hash',now()) RETURNING id::text`).Scan(&userA); err != nil {
		t.Fatal(err)
	}
	if err = env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Ana Ficticia','30111222') RETURNING id::text`, schoolA).Scan(&rosterA); err != nil {
		t.Fatal(err)
	}
	if err = env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Beto Ficticio','30111223') RETURNING id::text`, schoolB).Scan(&rosterB); err != nil {
		t.Fatal(err)
	}

	var dueA string
	if err = db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO finance_dues(tenant_id,name,amount_cents,due_date,created_by) VALUES($1,'Cuota de noviembre',200000,'2026-11-30',$2) RETURNING id::text`, schoolA, userA).Scan(&dueA); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO finance_due_members(tenant_id,due_id,roster_member_id) VALUES($1,$2,$3)`, schoolA, dueA, rosterA)
		return err
	}); err != nil {
		t.Fatalf("insert due in tenant A: %v", err)
	}

	// Check constraints reject blank name and non-positive amount.
	var pe *pgconn.PgError
	for _, tc := range []struct {
		name   string
		title  string
		amount int64
	}{
		{"blank name", "   ", 200000},
		{"zero amount", "Cuota", 0},
		{"negative amount", "Cuota", -100},
	} {
		err = db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO finance_dues(tenant_id,name,amount_cents,due_date,created_by) VALUES($1,$2,$3,'2026-11-30',$4)`, schoolA, tc.title, tc.amount, userA)
			return err
		})
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Fatalf("%s: got %v, want check_violation (23514)", tc.name, err)
		}
	}

	// Tenant isolation (RLS) and cross-tenant composite FK.
	if err = db.WithTenant(ctx, env.App, schoolB, func(tx pgx.Tx) error {
		var duesCount, membersCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM finance_dues`).Scan(&duesCount); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM finance_due_members`).Scan(&membersCount); err != nil {
			return err
		}
		if duesCount != 0 || membersCount != 0 {
			t.Fatalf("RLS leaked rows to schoolB: dues=%d members=%d", duesCount, membersCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err = db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO finance_dues(tenant_id,name,amount_cents,due_date,created_by) VALUES($1,'Otra',1000,'2026-11-30',$2)`, schoolB, userA)
		return err
	})
	if !errors.As(err, &pe) || pe.Code != "42501" {
		t.Fatalf("cross-tenant RLS insert: got %v, want 42501", err)
	}

	err = db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO finance_due_members(tenant_id,due_id,roster_member_id) VALUES($1,$2,$3)`, schoolA, dueA, rosterB)
		return err
	})
	if !errors.As(err, &pe) || pe.Code != "23503" {
		t.Fatalf("cross-tenant roster FK: got %v, want 23503", err)
	}

	// R7: Deleting a roster member must NOT cascade-delete due history.
	if _, err = env.Super.Exec(ctx, `DELETE FROM roster_members WHERE tenant_id=$1 AND id=$2`, schoolA, rosterA); !errors.As(err, &pe) || pe.Code != "23503" {
		t.Fatalf("roster_members delete without cascade: got %v, want 23503", err)
	}

	// Clean up rows, test Down and Up.
	if err = db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM finance_due_members WHERE tenant_id=$1 AND due_id=$2`, schoolA, dueA); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM finance_dues WHERE tenant_id=$1 AND id=$2`, schoolA, dueA)
		return err
	}); err != nil {
		t.Fatalf("delete due as ce_app: %v", err)
	}

	// DownTo/UpTo, not unbounded Up: later or earlier migrations must not shift the version this test rolls back.
	if _, err = p.DownTo(ctx, 23); err != nil {
		t.Fatalf("down to 23: %v", err)
	}
	var exists bool
	if err = env.Super.QueryRow(ctx, `SELECT to_regclass('public.finance_dues') IS NOT NULL OR to_regclass('public.finance_due_members') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("tables still exist after down: exists=%v err=%v", exists, err)
	}
	if _, err = p.UpTo(ctx, 25); err != nil {
		t.Fatalf("reapply 00025: %v", err)
	}
}
