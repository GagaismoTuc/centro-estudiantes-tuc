package finance_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

// TestReversalMigration checks, as the app role, the database guarantees of 00019 and that its
// Down and Up apply.
func TestReversalMigration(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00019_reversals.sql"); err != nil {
		t.Fatalf("reversal migration 00019 is missing: %v", err)
	}
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	var a, b, user, catA, catB string
	for _, dst := range []*string{&a, &b} {
		if err := env.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Fixture') RETURNING id::text`).Scan(dst); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, *dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES ('fixture-rev@example.invalid','x') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for tenant, dst := range map[string]*string{a: &catA, b: &catB} {
		if err := env.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas'`, tenant).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	// insert writes one entry of tenant as the app role; reverses is "" for a plain entry.
	insert := func(tenant, category, reverses, description string) (id string, err error) {
		err = db.WithTenant(ctx, env.App, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name,reverses_id) VALUES ($1,$2,'income','2026-11-14',100,$3,$4,'Ana Ficticia',NULLIF($5,'')::uuid) RETURNING id::text`, tenant, category, description, user, reverses).Scan(&id)
		})
		return
	}
	wantCode := func(t *testing.T, err error, code string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("want SQLSTATE %s, got %v", code, err)
		}
	}
	original, err := insert(a, catA, "", "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	reversal, err := insert(a, catA, original, "Reversión: Fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("S2 one reversal per entry", func(t *testing.T) {
		_, err := insert(a, catA, original, "Reversión: Fixture")
		wantCode(t, err, "23505")
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName != "finance_entries_reverses_id_key" {
			t.Fatalf("constraint=%s", pgErr.ConstraintName)
		}
	})
	t.Run("S3 a reversal cannot target a reversal or itself", func(t *testing.T) {
		_, err := insert(a, catA, reversal, "Reversión: Reversión: Fixture")
		wantCode(t, err, "23503")
		err = db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO finance_entries(id,tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name,reverses_id) VALUES ('11111111-1111-4111-8111-111111111111',$1,$2,'income','2026-11-14',100,'Fixture',$3,'Ana Ficticia','11111111-1111-4111-8111-111111111111')`, a, catA, user)
			return err
		})
		wantCode(t, err, "23503")
	})
	t.Run("S8 a reversal cannot target another school", func(t *testing.T) {
		target, err := insert(a, catA, "", "Fixture")
		if err != nil {
			t.Fatal(err)
		}
		_, err = insert(b, catB, target, "Reversión: Fixture")
		wantCode(t, err, "23503")
	})
	t.Run("S1 reversal description may carry the prefix over 500 characters", func(t *testing.T) {
		long := strings.Repeat("x", 500)
		target, err := insert(a, catA, "", long)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := insert(a, catA, target, "Reversión: "+long); err != nil {
			t.Fatalf("long reversal: %v", err)
		}
		_, err = insert(a, catA, "", long+"x")
		wantCode(t, err, "23514")
	})
	t.Run("entries stay append-only for the app role", func(t *testing.T) {
		for _, stmt := range []string{"UPDATE finance_entries SET reverses_id = NULL", "DELETE FROM finance_entries"} {
			err := db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, stmt); return err })
			wantCode(t, err, "42501")
		}
	})
	t.Run("Down and Up apply", func(t *testing.T) {
		if _, err := env.Super.Exec(ctx, `DELETE FROM finance_entries WHERE char_length(description) > 500`); err != nil {
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
		if _, err := p.DownTo(ctx, 18); err != nil {
			t.Fatalf("down: %v", err)
		}
		var n int
		if err := env.Super.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='finance_entries' AND column_name LIKE 'revers%'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("columns after down: n=%d err=%v", n, err)
		}
		if _, err := p.Up(ctx); err != nil {
			t.Fatalf("up: %v", err)
		}
		if _, err := insert(a, catA, original, "Reversión: Fixture"); err != nil {
			t.Fatalf("reversal after re-up: %v", err)
		}
	})
}
