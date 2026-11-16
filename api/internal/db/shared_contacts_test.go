package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

func TestSharedContactsMigrationReverses(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE shared_contacts_migration_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/shared_contacts_migration_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 24); err != nil {
		t.Fatalf("up: %v", err)
	}

	var scExists, sclExists bool
	if err := sqlDB.QueryRowContext(ctx, `SELECT to_regclass('shared_contacts') IS NOT NULL, to_regclass('shared_contact_links') IS NOT NULL`).Scan(&scExists, &sclExists); err != nil {
		t.Fatal(err)
	}
	if !scExists || !sclExists {
		t.Fatalf("after up: shared_contacts=%v shared_contact_links=%v, want both true", scExists, sclExists)
	}

	if _, err := p.DownTo(ctx, 23); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx, `SELECT to_regclass('shared_contacts') IS NOT NULL, to_regclass('shared_contact_links') IS NOT NULL`).Scan(&scExists, &sclExists); err != nil {
		t.Fatal(err)
	}
	if scExists || sclExists {
		t.Fatalf("after down: shared_contacts=%v shared_contact_links=%v, want both false", scExists, sclExists)
	}
}

func TestSharedContactsRLSIsolation(t *testing.T) {
	ctx := integration(t)
	for _, table := range []string{"shared_contacts", "shared_contact_links"} {
		var forced bool
		if err := env.super.QueryRow(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid = $1::regclass`, table).Scan(&forced); err != nil || !forced {
			t.Fatalf("%s forced RLS = %v (err=%v), want true", table, forced, err)
		}
	}

	var headingA, headingB, contactA, contactB string
	if err := env.super.QueryRow(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, 'gazebos') RETURNING id::text`, env.tenantA).Scan(&headingA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, 'gazebos') RETURNING id::text`, env.tenantB).Scan(&headingB); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contacts (tenant_id, heading_id, name, notes) VALUES ($1, $2, 'Gazebos A', 'Notas privadas A') RETURNING id::text`, env.tenantA, headingA).Scan(&contactA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contacts (tenant_id, heading_id, name) VALUES ($1, $2, 'Gazebos B') RETURNING id::text`, env.tenantB, headingB).Scan(&contactB); err != nil {
		t.Fatal(err)
	}

	var pendingA, approvedA, rejectedA, removedA string
	if err := env.super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name, zone) VALUES ('pending', 'gazebos', 'Pendiente A', 'Yerba Buena') RETURNING id::text`).Scan(&pendingA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name, zone) VALUES ('approved', 'gazebos', 'Aprobado A', 'Yerba Buena') RETURNING id::text`).Scan(&approvedA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name, zone) VALUES ('rejected', 'gazebos', 'Rechazado A', 'Yerba Buena') RETURNING id::text`).Scan(&rejectedA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name, zone) VALUES ('removed', 'gazebos', 'Baja A', 'Yerba Buena') RETURNING id::text`).Scan(&removedA); err != nil {
		t.Fatal(err)
	}
	for _, scID := range []string{pendingA, approvedA, rejectedA, removedA} {
		if _, err := env.super.Exec(ctx, `INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id) VALUES ($1, $2, $3)`, env.tenantA, contactA, scID); err != nil {
			t.Fatal(err)
		}
	}

	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id IN ($1, $2, $3, $4)`, pendingA, approvedA, rejectedA, removedA)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contacts WHERE tenant_id IN ($1, $2)`, env.tenantA, env.tenantB)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contact_headings WHERE id IN ($1, $2)`, headingA, headingB)
	})

	// Without tenant set: ce_app sees only approved rows and 0 links.
	var count int
	if err := env.app.QueryRow(ctx, `SELECT count(*) FROM shared_contacts WHERE id IN ($1, $2, $3, $4)`, pendingA, approvedA, rejectedA, removedA).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared_contacts without tenant: count=%d err=%v, want 1 (approved only)", count, err)
	}
	if err := env.app.QueryRow(ctx, `SELECT count(*) FROM shared_contact_links`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("shared_contact_links without tenant: count=%d err=%v, want 0", count, err)
	}

	// R8: with ce_app and tenant B, SELECT of pending and rejected (and removed) of A returns 0 rows,
	// SELECT of A's link returns 0 rows, and UPDATE to approved fails.
	err := db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
		var hidden int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM shared_contacts WHERE id IN ($1, $2, $3)`, pendingA, rejectedA, removedA).Scan(&hidden); err != nil {
			return err
		}
		if hidden != 0 {
			return fmt.Errorf("tenant B saw %d non-approved shared_contacts of tenant A", hidden)
		}
		var visible int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM shared_contacts WHERE id = $1`, approvedA).Scan(&visible); err != nil {
			return err
		}
		if visible != 1 {
			return fmt.Errorf("tenant B saw %d approved rows, want 1", visible)
		}
		var links int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM shared_contact_links WHERE shared_contact_id IN ($1, $2, $3, $4)`, pendingA, approvedA, rejectedA, removedA).Scan(&links); err != nil {
			return err
		}
		if links != 0 {
			return fmt.Errorf("tenant B saw %d links of tenant A", links)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tenant B read isolation: %v", err)
	}

	// ce_app cannot UPDATE status to approved or DELETE from shared_contacts.
	err = db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE shared_contacts SET status = 'approved' WHERE id = $1`, pendingA)
		return err
	})
	wantInsufficientPrivilege(t, err)

	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM shared_contacts WHERE id = $1`, pendingA)
		return err
	})
	wantInsufficientPrivilege(t, err)

	// ce_app cannot INSERT an already-approved shared_contact.
	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO shared_contacts (status, heading_name, name) VALUES ('approved', 'gazebos', 'Bypass')`)
		return err
	})
	wantRLSError(t, err)

	// ce_app cannot INSERT a link with tenant A while scoped to tenant B.
	err = db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id) VALUES ($1, $2, $3)`, env.tenantA, contactA, approvedA)
		return err
	})
	wantRLSError(t, err)

	// Cross-tenant composite FK rejection: tenant B cannot link to tenant A's contact.
	err = db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
		var newID string
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&newID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shared_contacts (id, status, heading_name, name) VALUES ($1, 'pending', 'gazebos', 'Cross')`, newID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id) VALUES ($1, $2, $3)`, env.tenantB, contactA, newID)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant link FK = %v, want 23503", err)
	}
}

func wantInsufficientPrivilege(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("err = %v, want SQLSTATE 42501", err)
	}
}
