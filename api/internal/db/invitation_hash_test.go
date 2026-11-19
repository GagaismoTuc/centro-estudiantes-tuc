package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
)

// An invitation hash opens exactly one row and never grants access to another school.
func TestInvitationHashIsolationAndPrivileges(t *testing.T) {
	ctx := integration(t)
	mine, sameSchool, otherSchool := randomHash(), randomHash(), randomHash()
	for i, row := range []struct {
		tenant string
		hash   []byte
	}{{env.tenantA, mine}, {env.tenantA, sameSchool}, {env.tenantB, otherSchool}} {
		if _, err := env.super.Exec(ctx, `INSERT INTO school_invitations(tenant_id, token_hash, email, role, created_at, expires_at) VALUES($1,$2,$3,'veedor',now(),now()+interval '48 hours')`, row.tenant, row.hash, []string{"a@example.test", "b@example.test", "c@example.test"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM school_invitations WHERE token_hash IN ($1,$2,$3)`, mine, sameSchool, otherSchool)
	})
	var visible int
	if err := env.app.QueryRow(ctx, `SELECT count(*) FROM school_invitations`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("without tenant or token: %d %v", visible, err)
	}
	fresh := randomHash()
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM school_invitations WHERE token_hash=$1`, fresh)
	})
	if err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO school_invitations(tenant_id,token_hash,email,role,created_at,expires_at) VALUES($1,$2,'fresh@example.test','veedor',now(),now()+interval '48 hours')`, env.tenantA, fresh)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := db.WithInvitationHash(ctx, env.app, mine, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM school_invitations`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("visible invitations = %d", n)
		}
		tag, err := tx.Exec(ctx, `UPDATE school_invitations SET used_at=now()`)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			t.Fatalf("updated = %d", tag.RowsAffected())
		}
		if err := db.SetTenant(ctx, tx, env.tenantA); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM roster_members WHERE tenant_id=$1`, env.tenantB).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("other school rows = %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var used int
	if err := env.super.QueryRow(ctx, `SELECT count(*) FROM school_invitations WHERE used_at IS NOT NULL`).Scan(&used); err != nil || used != 1 {
		t.Fatalf("used = %d, %v", used, err)
	}
	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE school_invitations SET role='school_admin' WHERE token_hash=$1`, sameSchool)
		return err
	})
	if err == nil {
		t.Fatal("application changed an invitation role")
	}
}

// The migration applies and rolls back only after Veedor memberships are removed.
func TestVeedorInvitationMigrationReverses(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE veedor_migration_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/veedor_migration_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 39); err != nil {
		t.Fatalf("up: %v", err)
	}
	var table bool
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass('public.school_invitations') IS NOT NULL`).Scan(&table); err != nil || !table {
		t.Fatalf("invitation table=%v err=%v", table, err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO organizations(id,kind,name) VALUES('00000000-0000-4000-8000-000000000001','school','Fixture'); INSERT INTO users(id,email,password_hash) VALUES('00000000-0000-4000-8000-000000000002','fixture@example.test','x'); INSERT INTO memberships(tenant_id,user_id,role) VALUES('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002','veedor')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 38); err == nil {
		t.Fatal("down removed support for a live Veedor")
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM memberships WHERE role='veedor'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 38); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass('public.school_invitations') IS NOT NULL`).Scan(&table); err != nil || table {
		t.Fatalf("table after down=%v err=%v", table, err)
	}
	if _, err := p.UpTo(ctx, 39); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

