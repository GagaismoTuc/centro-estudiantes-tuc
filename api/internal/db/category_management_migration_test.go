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

func TestCategoryManagementMigration00020(t *testing.T) {
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
	var a, b string
	for _, target := range []*string{&a, &b} {
		if err = env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Fixture') RETURNING id::text`).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = env.Super.Exec(ctx, `SELECT seed_finance_categories($1),seed_finance_categories($2)`, a, b); err != nil {
		t.Fatal(err)
	}
	var reserved bool
	if err = env.Super.QueryRow(ctx, `SELECT reserved FROM finance_categories WHERE tenant_id=$1 AND direction='income' AND name='Cuotas'`, a).Scan(&reserved); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v", reserved, err)
	}
	insert := func(tenant, name, direction string) error {
		return db.WithTenant(ctx, env.App, tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO finance_categories(tenant_id,name,direction) VALUES($1,$2,$3)`, tenant, name, direction)
			return err
		})
	}
	var pe *pgconn.PgError
	if err = insert(a, "  ventas ", "income"); !errors.As(err, &pe) || pe.Code != "23505" {
		t.Fatalf("normalized duplicate: %v", err)
	}
	if err = insert(b, "Buzos", "expense"); err != nil {
		t.Fatal(err)
	}
	if err = insert(a, "Buzos", "expense"); err != nil {
		t.Fatal(err)
	}
	if err = db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE finance_categories SET name='Kiosco' WHERE tenant_id=$1 AND name='Ventas'`, a); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM finance_categories WHERE tenant_id=$1 AND name='Buzos'`, a)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 19); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err = insert(a, "  kiosco ", "income"); err != nil {
		t.Fatalf("down did not remove normalized index: %v", err)
	}
	if err = db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE finance_categories SET name='X' WHERE tenant_id=$1 AND name='Kiosco'`, a)
		return err
	}); !errors.As(err, &pe) || pe.Code != "42501" {
		t.Fatalf("down did not revoke update: %v", err)
	}
	if _, err = env.Super.Exec(ctx, `DELETE FROM finance_categories WHERE tenant_id=$1 AND name='  kiosco '`, a); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpTo(ctx, 20); err != nil {
		t.Fatalf("reapply: %v", err)
	}
}
