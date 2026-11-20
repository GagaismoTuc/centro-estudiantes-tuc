package finance_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

// TestFinanceEntryEventBudgetMigration checks, as the app role, the database guarantees of
// migration 00037 (R1, R2, R7): both-or-neither CHECK, composite tenant FKs, RESTRICT delete,
// unchanged append-only grants, and Down/Up round-trip.
func TestFinanceEntryEventBudgetMigration(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00037_finance_entry_event_budget.sql"); err != nil {
		t.Fatalf("migration 00037 is missing: %v", err)
	}
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	var a, b, user, catA, catB, budgetA, budgetB, headingA, headingB string
	for _, dst := range []*string{&a, &b} {
		if err := env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Fixture') RETURNING id::text`).Scan(dst); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, *dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES ('fixture-budget-link@example.invalid','x') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for tenant, cat := range map[string]*string{a: &catA, b: &catB} {
		if err := env.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Alquileres'`, tenant).Scan(cat); err != nil {
			t.Fatal(err)
		}
	}
	for tenant, dst := range map[string]*string{a: &budgetA, b: &budgetB} {
		if err := env.Super.QueryRow(ctx, `INSERT INTO event_budgets(tenant_id,name,starts_on,ends_on) VALUES ($1,'Semana del Estudiante 2026','2026-09-21','2026-09-25') RETURNING id::text`, tenant).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	for tenant, dst := range map[string]*string{a: &headingA, b: &headingB} {
		if err := env.Super.QueryRow(ctx, `INSERT INTO contact_headings(tenant_id,name) VALUES ($1,'mesas y sillas') RETURNING id::text`, tenant).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}

	insert := func(tenant, category, budgetID, headingID string) (id string, err error) {
		err = db.WithTenant(ctx, env.App, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name,event_budget_id,heading_id)
				VALUES ($1,$2,'expense','2026-11-14',11000000,'Alquiler de sillas',$3,'Ana Ficticia',NULLIF($4,'')::uuid,NULLIF($5,'')::uuid)
				RETURNING id::text`, tenant, category, user, budgetID, headingID).Scan(&id)
		})
		return
	}
	wantCode := func(t *testing.T, err error, code, constraint string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("want SQLSTATE %s, got %v", code, err)
		}
		if constraint != "" && pgErr.ConstraintName != constraint {
			t.Fatalf("want constraint %s, got %s", constraint, pgErr.ConstraintName)
		}
	}

	t.Run("R1 accepts both null or both set in same tenant", func(t *testing.T) {
		if _, err := insert(a, catA, "", ""); err != nil {
			t.Fatalf("both null: %v", err)
		}
		if _, err := insert(a, catA, budgetA, headingA); err != nil {
			t.Fatalf("both set: %v", err)
		}
	})

	t.Run("R2 pair CHECK rejects only one of event_budget_id or heading_id", func(t *testing.T) {
		_, err := insert(a, catA, budgetA, "")
		wantCode(t, err, "23514", "finance_entries_event_link_pair_check")
		_, err = insert(a, catA, "", headingA)
		wantCode(t, err, "23514", "finance_entries_event_link_pair_check")
	})

	t.Run("R7 composite FKs reject cross-tenant budget or heading", func(t *testing.T) {
		_, err := insert(a, catA, budgetB, headingA)
		wantCode(t, err, "23503", "finance_entries_event_budget_fkey")
		_, err = insert(a, catA, budgetA, headingB)
		wantCode(t, err, "23503", "finance_entries_heading_fkey")
		// Even if both belong to tenant B, tenant A cannot reference them.
		_, err = insert(a, catA, budgetB, headingB)
		wantCode(t, err, "23503", "")
	})

	t.Run("R5 RESTRICT blocks deleting event_budget or heading with linked entries", func(t *testing.T) {
		err := db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM event_budgets WHERE tenant_id=$1 AND id=$2`, a, budgetA)
			return err
		})
		wantCode(t, err, "23001", "finance_entries_event_budget_fkey")

		err = db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM contact_headings WHERE tenant_id=$1 AND id=$2`, a, headingA)
			return err
		})
		wantCode(t, err, "23001", "finance_entries_heading_fkey")
	})

	t.Run("entries stay append-only for ce_app", func(t *testing.T) {
		for _, stmt := range []string{
			"UPDATE finance_entries SET event_budget_id = NULL, heading_id = NULL",
			"DELETE FROM finance_entries",
		} {
			err := db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, stmt)
				return err
			})
			wantCode(t, err, "42501", "")
		}
	})

	t.Run("Down and Up apply cleanly", func(t *testing.T) {
		conn, err := sql.Open("pgx", env.SuperDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
		if err != nil {
			t.Fatal(err)
		}
		// Down/Up pinned between the latest earlier migration and 00037 so earlier
		// (e.g. 00036) or later migrations never shift what this test rolls back.
		var prevVersion int64
		for _, src := range p.ListSources() {
			if src.Version < 37 && src.Version > prevVersion {
				prevVersion = src.Version
			}
		}
		if prevVersion == 0 {
			t.Fatal("no migration found before 00037")
		}
		if _, err := p.DownTo(ctx, prevVersion); err != nil {
			t.Fatalf("down to %d: %v", prevVersion, err)
		}
		var n int
		if err := env.Super.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='finance_entries' AND column_name IN ('event_budget_id','heading_id')`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("columns after down: n=%d err=%v", n, err)
		}
		if _, err := p.UpTo(ctx, 37); err != nil {
			t.Fatalf("up to 37: %v", err)
		}
		if _, err := insert(a, catA, budgetA, headingA); err != nil {
			t.Fatalf("insert after re-up: %v", err)
		}
	})
}
