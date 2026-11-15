package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

func TestContactsMigrationSeedsExistingSchoolsAndReverses(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE contacts_migration_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/contacts_migration_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	var schoolID, muniID string
	if err := sqlDB.QueryRowContext(ctx, `INSERT INTO organizations (kind, name) VALUES ('school', 'Escuela Existente') RETURNING id::text`).Scan(&schoolID); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRowContext(ctx, `INSERT INTO organizations (kind, name) VALUES ('municipality', 'Tucumán') RETURNING id::text`).Scan(&muniID); err != nil {
		t.Fatal(err)
	}

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	rows, err := sqlDB.QueryContext(ctx, `SELECT name FROM contact_headings WHERE tenant_id = $1 ORDER BY name`, schoolID)
	if err != nil {
		t.Fatalf("query headings: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"artistas",
		"cristalería",
		"foodtrucks",
		"gazebos",
		"impresiones",
		"insumos plásticos",
		"mesas y sillas",
		"otros",
		"seguridad",
		"sonido e iluminación",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("seeded school headings = %v, want %v", got, want)
	}

	var muniCount int
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM contact_headings WHERE tenant_id = $1`, muniID).Scan(&muniCount); err != nil || muniCount != 0 {
		t.Fatalf("municipality headings = %d (err=%v), want 0", muniCount, err)
	}

	if _, err := p.DownTo(ctx, 10); err != nil {
		t.Fatalf("down: %v", err)
	}
	var h, c, rp bool
	if err := sqlDB.QueryRowContext(ctx, `SELECT to_regclass('contact_headings') IS NOT NULL, to_regclass('contacts') IS NOT NULL, to_regclass('contact_reference_prices') IS NOT NULL`).Scan(&h, &c, &rp); err != nil {
		t.Fatal(err)
	}
	if h || c || rp {
		t.Fatalf("after down: headings=%v contacts=%v prices=%v, want all false", h, c, rp)
	}
}

func TestContactsTablesFailClosedAndRejectCrossTenantFK(t *testing.T) {
	ctx := integration(t)
	for _, table := range []string{"contact_headings", "contacts", "contact_reference_prices"} {
		var forced bool
		if err := env.super.QueryRow(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid = $1::regclass`, table).Scan(&forced); err != nil || !forced {
			t.Fatalf("%s forced RLS = %v (err=%v), want true", table, forced, err)
		}
	}

	var headingA, headingB, contactB string
	if err := env.super.QueryRow(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, 'gazebos') RETURNING id::text`, env.tenantA).Scan(&headingA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, 'gazebos') RETURNING id::text`, env.tenantB).Scan(&headingB); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contacts (tenant_id, heading_id, name) VALUES ($1, $2, 'Gazebos B') RETURNING id::text`, env.tenantB, headingB).Scan(&contactB); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx, `INSERT INTO contact_reference_prices (tenant_id, contact_id, description, amount_cents, quoted_on) VALUES ($1, $2, 'Gazebo 3x3', 2500000, '2026-09-01')`, env.tenantB, contactB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contacts WHERE tenant_id IN ($1, $2)`, env.tenantA, env.tenantB)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contact_headings WHERE id IN ($1, $2)`, headingA, headingB)
	})

	// Fail closed without tenant set.
	for _, table := range []string{"contact_headings", "contacts", "contact_reference_prices"} {
		var count int
		if err := env.app.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s without tenant: count=%d err=%v, want 0", table, count, err)
		}
	}

	// Tenant A cannot read tenant B's contacts/prices or insert with tenant B's tenant_id.
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE id = $1`, contactB).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("saw %d contacts of tenant B", count)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM contact_reference_prices WHERE contact_id = $1`, contactB).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("saw %d reference prices of tenant B", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tenant A read isolation: %v", err)
	}

	// Cross-tenant RLS insert rejection.
	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, 'Carpa')`, env.tenantB)
		return err
	})
	wantRLSError(t, err)

	// Cross-tenant composite FK rejection: tenant A cannot reference tenant B's heading or contact.
	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contacts (tenant_id, heading_id, name) VALUES ($1, $2, 'Cross')`, env.tenantA, headingB)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant heading FK = %v, want 23503", err)
	}

	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contact_reference_prices (tenant_id, contact_id, description, amount_cents, quoted_on) VALUES ($1, $2, 'Cross', 100, '2026-09-01')`, env.tenantA, contactB)
		return err
	})
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant contact FK = %v, want 23503", err)
	}

	_ = headingA
}
