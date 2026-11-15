package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

// TestFinanceMigration exercises an upgrade of an existing school, then the app role.
func TestFinanceMigration(t *testing.T) {
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:18-alpine", postgres.WithDatabase("finance"), postgres.WithUsername("postgres"), postgres.WithPassword("postgres"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	defer ctr.Terminate(ctx)
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	var a, b string
	if err = conn.QueryRowContext(ctx, `INSERT INTO organizations (kind,name) VALUES ('school','School A') RETURNING id::text`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(ctx, `INSERT INTO organizations (kind,name) VALUES ('school','School B') RETURNING id::text`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var userID, categoryB string
	if err = conn.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES ('fixture@example.invalid','fixture') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas'`, b).Scan(&categoryB); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO finance_entries(tenant_id, category_id, direction, entry_date, amount_cents, description, recorded_by, recorded_by_name) VALUES ($1,$2,'income','2026-11-10',100,'Fixture',$3,'Ana Ficticia')`, b, categoryB, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `CREATE ROLE finance_test LOGIN PASSWORD 'finance_test' IN ROLE ce_app`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User, cfg.ConnConfig.Password = "finance_test", "finance_test"
	app, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	t.Run("R6 existing schools receive exact default categories", func(t *testing.T) {
		want := map[string]bool{"income:Cuotas": true, "income:Eventos": true, "income:Ventas": true, "income:Donaciones y sponsors": true, "income:Otros ingresos": true, "expense:Eventos": true, "expense:Materiales e impresiones": true, "expense:Alquileres": true, "expense:Viajes": true, "expense:Otros gastos": true}
		for _, tenant := range []string{a, b} {
			got := map[string]bool{}
			err := db.WithTenant(ctx, app, tenant, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, `SELECT direction, name FROM finance_categories`)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var direction, name string
					if err := rows.Scan(&direction, &name); err != nil {
						return err
					}
					got[direction+":"+name] = true
				}
				return rows.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("categories: got %v want %v", got, want)
			}
			for k := range want {
				if !got[k] {
					t.Errorf("missing %s", k)
				}
			}
		}
	})
	t.Run("R8 app role cannot update or delete entries", func(t *testing.T) {
		for _, verb := range []string{"UPDATE finance_entries SET amount_cents = 1", "DELETE FROM finance_entries"} {
			err := db.WithTenant(ctx, app, a, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, verb); return err })
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Errorf("%s: expected permission denied, got %v", verb, err)
			}
		}
	})
	t.Run("R12 tables fail closed and isolate tenants", func(t *testing.T) {
		for _, table := range []string{"finance_categories", "finance_entries"} {
			var n int
			if err := app.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
				t.Fatalf("without tenant %s: n=%d err=%v", table, n, err)
			}
			err := db.WithTenant(ctx, app, a, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, b).Scan(&n)
			})
			if err != nil || n != 0 {
				t.Fatalf("other tenant %s: n=%d err=%v", table, n, err)
			}
		}
		err := db.WithTenant(ctx, app, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO finance_entries(tenant_id, category_id, direction, entry_date, amount_cents, description, recorded_by, recorded_by_name) VALUES ($1,$2,'income','2026-11-10',100,'Fixture',$3,'Ana Ficticia')`, a, categoryB, userID)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("cross-tenant FK: %v", err)
		}
	})
	t.Run("R2 database rejects nonpositive amounts", func(t *testing.T) {
		for _, amount := range []int{0, -50000} {
			err := db.WithTenant(ctx, app, b, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO finance_entries(tenant_id, category_id, direction, entry_date, amount_cents, description, recorded_by, recorded_by_name) VALUES ($1,$2,'income','2026-11-10',$3,'Fixture',$4,'Ana Ficticia')`, b, categoryB, amount, userID)
				return err
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Errorf("amount %d: %v", amount, err)
			}
		}
	})
	if _, err = p.Down(ctx); err != nil {
		t.Fatalf("down: %v", err)
	}
}
