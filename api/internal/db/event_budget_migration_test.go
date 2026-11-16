package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

func TestEventBudgetMigrationTenantIsolationAndForeignKeys(t *testing.T) {
	ctx := integration(t)
	for _, table := range []string{"event_budgets", "event_budget_lines"} {
		var forced bool
		if err := env.super.QueryRow(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid = $1::regclass`, table).Scan(&forced); err != nil || !forced {
			t.Fatalf("%s forced RLS = %v, err = %v", table, forced, err)
		}
		var count int
		if err := env.app.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s fail closed count = %d, err = %v", table, count, err)
		}
	}
	var budgetA, budgetB, headingB, contactB string
	for _, v := range []struct {
		tenant string
		id     *string
	}{{env.tenantA, &budgetA}, {env.tenantB, &budgetB}} {
		if err := env.super.QueryRow(ctx, `INSERT INTO event_budgets (tenant_id, name, starts_on, ends_on) VALUES ($1, 'Week', '2026-09-21', '2026-09-25') RETURNING id::text`, v.tenant).Scan(v.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contact_headings (tenant_id, name) VALUES ($1, 'budget test heading') RETURNING id::text`, env.tenantB).Scan(&headingB); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `INSERT INTO contacts (tenant_id, heading_id, name) VALUES ($1, $2, 'budget test contact') RETURNING id::text`, env.tenantB, headingB).Scan(&contactB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM event_budgets WHERE id IN ($1, $2)`, budgetA, budgetB)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contacts WHERE id=$1`, contactB)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM contact_headings WHERE id=$1`, headingB)
	})
	if _, err := env.super.Exec(ctx, `INSERT INTO event_budget_lines (tenant_id,budget_id,kind,description,quantity,unit_amount_cents) VALUES ($1,$2,'sponsor','Sponsor',1,100)`, env.tenantB, budgetB); err != nil {
		t.Fatal(err)
	}
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		for _, table := range []string{"event_budgets", "event_budget_lines"} {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id=$1`, env.tenantB).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("%s exposes %d rows", table, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []struct{ budget, heading, contact any }{{budgetB, nil, nil}, {budgetA, headingB, nil}, {budgetA, nil, contactB}} {
		err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO event_budget_lines (tenant_id,budget_id,heading_id,contact_id,kind,description,quantity,unit_amount_cents) VALUES ($1,$2,$3,$4,'cost','Test',1,100)`, env.tenantA, values.budget, values.heading, values.contact)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("cross-tenant FK = %v, want 23503", err)
		}
	}
}

func TestEventBudgetMigrationUpAndDown(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE event_budget_migration_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/event_budget_migration_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 23); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, 22); err != nil {
		t.Fatalf("down: %v", err)
	}
	var budgets, lines bool
	if err := sqlDB.QueryRowContext(ctx, `SELECT to_regclass('event_budgets') IS NOT NULL, to_regclass('event_budget_lines') IS NOT NULL`).Scan(&budgets, &lines); err != nil || budgets || lines {
		t.Fatalf("after down: budgets=%v lines=%v err=%v", budgets, lines, err)
	}
}
