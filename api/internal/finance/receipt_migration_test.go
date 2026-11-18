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

// TestReceiptMigration checks, as the app role, the database guarantees of 00028 and that its
// Down and Up apply cleanly.
func TestReceiptMigration(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00028_finance_receipts.sql"); err != nil {
		t.Fatalf("receipt migration 00028 is missing: %v", err)
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
	if err := env.Super.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES ('fixture-rec@example.invalid','x') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for tenant, dst := range map[string]*string{a: &catA, b: &catB} {
		if err := env.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas'`, tenant).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}

	insertEntry := func(tenant, category string) string {
		var id string
		if err := db.WithTenant(ctx, env.App, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name) VALUES ($1,$2,'income','2026-11-14',100,'Fixture',$3,'Ana Ficticia') RETURNING id::text`, tenant, category, user).Scan(&id)
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	entryA := insertEntry(a, catA)
	_ = insertEntry(b, catB)

	insertReceipt := func(tenant, entryID, contentType string, sizeBytes int, data []byte) (id string, err error) {
		err = db.WithTenant(ctx, env.App, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `INSERT INTO finance_receipts(tenant_id,entry_id,content_type,size_bytes,data,uploaded_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`, tenant, entryID, contentType, sizeBytes, data, user).Scan(&id)
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

	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	r1, err := insertReceipt(a, entryA, "image/png", len(png), png)
	if err != nil {
		t.Fatalf("insert first receipt: %v", err)
	}
	if _, err := insertReceipt(a, entryA, "application/pdf", 5, []byte("%PDF-")); err != nil {
		t.Fatalf("insert second receipt on same entry: %v", err)
	}

	t.Run("R8 cross-tenant entry reference rejected by composite FK and RLS hides rows", func(t *testing.T) {
		_, err := insertReceipt(b, entryA, "image/png", len(png), png)
		wantCode(t, err, "23503")
		var visible int
		if err := db.WithTenant(ctx, env.App, b, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM finance_receipts WHERE id=$1`, r1).Scan(&visible)
		}); err != nil || visible != 0 {
			t.Fatalf("tenant B saw tenant A receipt: visible=%d err=%v", visible, err)
		}
	})

	t.Run("R4 ce_app cannot update or delete receipts", func(t *testing.T) {
		for _, stmt := range []string{
			`UPDATE finance_receipts SET content_type = 'image/jpeg'`,
			`DELETE FROM finance_receipts`,
		} {
			err := db.WithTenant(ctx, env.App, a, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, stmt)
				return err
			})
			wantCode(t, err, "42501")
		}
	})

	t.Run("constraints enforce content type and size bounds", func(t *testing.T) {
		_, err := insertReceipt(a, entryA, "image/gif", 3, []byte("GIF"))
		wantCode(t, err, "23514")
		_, err = insertReceipt(a, entryA, "image/png", 0, []byte{})
		wantCode(t, err, "23514")
		_, err = insertReceipt(a, entryA, "image/png", 2, []byte{0x89})
		wantCode(t, err, "23514")
	})

	t.Run("Down and Up apply", func(t *testing.T) {
		conn, err := sql.Open("pgx", env.SuperDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.DownTo(ctx, 27); err != nil {
			t.Fatalf("down: %v", err)
		}
		var exists bool
		if err := env.Super.QueryRow(ctx, `SELECT to_regclass('public.finance_receipts') IS NOT NULL`).Scan(&exists); err != nil || exists {
			t.Fatalf("table still exists after down: exists=%v err=%v", exists, err)
		}
		if _, err := p.Up(ctx); err != nil {
			t.Fatalf("up: %v", err)
		}
		if _, err := insertReceipt(a, entryA, "image/jpeg", 3, []byte{0xFF, 0xD8, 0xFF}); err != nil {
			t.Fatalf("insert after re-up: %v", err)
		}
	})
}
