package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

func TestSharedContactsLifecycleMigration00035(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00035_shared_contacts_lifecycle.sql"); err != nil {
		t.Fatalf("shared contacts lifecycle migration 00035 is missing: %v", err)
	}
	ctx := integration(t)

	// Forced RLS on shared_contact_withdrawals and no widened UPDATE/DELETE grants on #77 tables.
	var rls, forced bool
	if err := env.super.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = 'public.shared_contact_withdrawals'::regclass`).Scan(&rls, &forced); err != nil {
		t.Fatalf("shared_contact_withdrawals missing: %v", err)
	}
	if !rls || !forced {
		t.Fatalf("shared_contact_withdrawals RLS enabled=%v forced=%v, want both true", rls, forced)
	}
	for _, tbl := range []string{"public.shared_contacts", "public.shared_contact_links", "public.shared_contact_withdrawals"} {
		for priv, want := range map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": false, "DELETE": false} {
			var has bool
			if err := env.super.QueryRow(ctx, `SELECT has_table_privilege('ce_app', $1, $2)`, tbl, priv).Scan(&has); err != nil {
				t.Fatal(err)
			}
			if has != want {
				t.Fatalf("ce_app %s on %s = %v, want %v", priv, tbl, has, want)
			}
		}
	}

	var headingA, contactA, approvedA, pendingA string
	if err := env.super.QueryRow(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, 'gazebos') RETURNING id::text`, env.tenantA).Scan(&headingA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contacts (tenant_id, heading_id, name) VALUES ($1, $2, 'Gazebos A') RETURNING id::text`, env.tenantA, headingA).Scan(&contactA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name) VALUES ('approved', 'gazebos', 'Gazebos A') RETURNING id::text`).Scan(&approvedA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name) VALUES ('pending', 'gazebos', 'Gazebos A Pendiente') RETURNING id::text`).Scan(&pendingA); err != nil {
		t.Fatal(err)
	}
	for _, scID := range []string{approvedA, pendingA} {
		if _, err := env.super.Exec(ctx, `INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id) VALUES ($1, $2, $3)`, env.tenantA, contactA, scID); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id IN ($1, $2)`, approvedA, pendingA)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contacts WHERE id = $1`, contactA)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contact_headings WHERE id = $1`, headingA)
	})

	// Tenant B cannot withdraw Tenant A's shared contact (neither via tenant B FK nor via tenant A RLS).
	err := db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO shared_contact_withdrawals (tenant_id, shared_contact_id) VALUES ($1, $2)`, env.tenantB, approvedA)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant withdrawal FK = %v, want 23503", err)
	}
	err = db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO shared_contact_withdrawals (tenant_id, shared_contact_id) VALUES ($1, $2)`, env.tenantA, approvedA)
		return err
	})
	wantRLSError(t, err)

	// Tenant A withdraws its approved shared contact; trigger moves status to withdrawn.
	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO shared_contact_withdrawals (tenant_id, shared_contact_id) VALUES ($1, $2)`, env.tenantA, approvedA)
		return err
	})
	if err != nil {
		t.Fatalf("tenant A withdraw approved: %v", err)
	}
	var st string
	if err := env.super.QueryRow(ctx, `SELECT status FROM shared_contacts WHERE id = $1`, approvedA).Scan(&st); err != nil || st != "withdrawn" {
		t.Fatalf("approvedA status after withdrawal = %q (err=%v), want withdrawn", st, err)
	}

	// Tenant B cannot read Tenant A's withdrawal row.
	err = db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM shared_contact_withdrawals`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return errors.New("tenant B saw tenant A withdrawal")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Down/Up reversibility pinned between latest earlier migration and 00035.
	conn, err := sql.Open("pgx", env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	var prevVersion int64
	for _, src := range p.ListSources() {
		if src.Version < 35 && src.Version > prevVersion {
			prevVersion = src.Version
		}
	}
	if prevVersion == 0 {
		t.Fatal("no migration found before 00035")
	}
	if _, err := p.DownTo(ctx, prevVersion); err != nil {
		t.Fatalf("down to %d: %v", prevVersion, err)
	}
	var withdrawalsExists bool
	if err := env.super.QueryRow(ctx, `SELECT to_regclass('public.shared_contact_withdrawals') IS NOT NULL`).Scan(&withdrawalsExists); err != nil {
		t.Fatal(err)
	}
	if withdrawalsExists {
		t.Fatalf("after down: shared_contact_withdrawals still exists")
	}
	if _, err := p.UpTo(ctx, 35); err != nil {
		t.Fatalf("reapply 00035: %v", err)
	}
	if err := env.super.QueryRow(ctx, `SELECT to_regclass('public.shared_contact_withdrawals') IS NOT NULL`).Scan(&withdrawalsExists); err != nil || !withdrawalsExists {
		t.Fatalf("after up: shared_contact_withdrawals=%v err=%v", withdrawalsExists, err)
	}
}
