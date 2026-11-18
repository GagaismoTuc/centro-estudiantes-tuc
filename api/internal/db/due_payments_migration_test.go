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

// TestDuePaymentsMigration00030 covers R12 at the database: append-only grants, forced RLS and
// composite foreign keys to the Due snapshot and to a non-Reversal Entry; plus the row lock of R5.
func TestDuePaymentsMigration00030(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00030_due_payments.sql"); err != nil {
		t.Fatalf("due payments migration 00030 is missing: %v", err)
	}
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

	var schoolA, schoolB, user, ana, beto, carla, otherSchool string
	seed := []struct {
		dst  *string
		sql  string
		args []any
	}{
		{&schoolA, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela A') RETURNING id::text`, nil},
		{&schoolB, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela B') RETURNING id::text`, nil},
		{&user, `INSERT INTO users(email,password_hash,email_verified_at) VALUES ('ana@a.example','hash',now()) RETURNING id::text`, nil},
	}
	for _, s := range seed {
		if err := env.Super.QueryRow(ctx, s.sql, s.args...).Scan(s.dst); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []struct {
		dst          *string
		tenant, name string
		doc          string
	}{
		{&ana, schoolA, "Ana Ficticia", "30111222"},
		{&beto, schoolA, "Beto Ficticio", "30111223"},
		{&carla, schoolA, "Carla Ficticia", "30111224"},
		{&otherSchool, schoolB, "Diego Ficticio", "30111225"},
	} {
		if err := env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,$2,$3) RETURNING id::text`, m.tenant, m.name, m.doc).Scan(m.dst); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, schoolA); err != nil {
		t.Fatal(err)
	}
	var cuotas string
	if err := env.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND direction='income' AND name='Cuotas'`, schoolA).Scan(&cuotas); err != nil {
		t.Fatal(err)
	}

	var due string
	if err := env.Super.QueryRow(ctx, `INSERT INTO finance_dues(tenant_id,name,amount_cents,due_date,created_by) VALUES($1,'Cuota de noviembre',200000,'2026-11-30',$2) RETURNING id::text`, schoolA, user).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Super.Exec(ctx, `INSERT INTO finance_due_members(tenant_id,due_id,roster_member_id) VALUES($1,$2,$3),($1,$2,$4)`, schoolA, due, ana, beto); err != nil {
		t.Fatal(err)
	}

	insertEntry := func(tx pgx.Tx) (string, error) {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
			VALUES ($1,$2,'income','2026-11-15',50000,'Pago de cuota: Cuota de noviembre',$3,'Ana Ficticia') RETURNING id::text`, schoolA, cuotas, user).Scan(&id)
		return id, err
	}
	insertPayment := func(tx pgx.Tx, member, entry string) error {
		_, err := tx.Exec(ctx, `INSERT INTO finance_due_payments(tenant_id,due_id,roster_member_id,entry_id,amount_cents,payment_date,recorded_by)
			VALUES ($1,$2,$3,$4,50000,'2026-11-15',$5)`, schoolA, due, member, entry, user)
		return err
	}

	// ce_app records a payment for a snapshot member, linked to its Entry.
	var paymentID string
	if err := db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		entry, err := insertEntry(tx)
		if err != nil {
			return err
		}
		if err := insertPayment(tx, beto, entry); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id::text FROM finance_due_payments WHERE entry_id=$1`, entry).Scan(&paymentID)
	}); err != nil {
		t.Fatalf("insert payment as ce_app: %v", err)
	}

	var pe *pgconn.PgError
	wantCode := func(name, code string, err error) {
		t.Helper()
		if !errors.As(err, &pe) || pe.Code != code {
			t.Fatalf("%s: got %v, want %s", name, err, code)
		}
	}

	// Append-only: ce_app cannot update or delete payments.
	wantCode("update payment", "42501", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE finance_due_payments SET amount_cents=1 WHERE id=$1`, paymentID)
		return err
	}))
	wantCode("delete payment", "42501", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM finance_due_payments WHERE id=$1`, paymentID)
		return err
	}))

	// A member outside the Due's snapshot (Carla, imported later) or of another school is rejected.
	for name, member := range map[string]string{"member not owing": carla, "member of other school": otherSchool} {
		wantCode(name, "23503", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
			entry, err := insertEntry(tx)
			if err != nil {
				return err
			}
			return insertPayment(tx, member, entry)
		}))
	}

	// The Entry must exist in the same school and must not be a Reversal.
	wantCode("reversal entry", "23503", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		entry, err := insertEntry(tx)
		if err != nil {
			return err
		}
		var reversal string
		if err := tx.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name,reverses_id)
			VALUES ($1,$2,'expense','2026-11-15',50000,'Reversión: Pago',$3,'Ana Ficticia',$4) RETURNING id::text`, schoolA, cuotas, user, entry).Scan(&reversal); err != nil {
			return err
		}
		return insertPayment(tx, beto, reversal)
	}))
	wantCode("missing entry", "23503", db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		return insertPayment(tx, beto, "00000000-0000-0000-0000-000000000000")
	}))

	// RLS: school B sees no payment and cannot insert one for school A.
	if err := db.WithTenant(ctx, env.App, schoolB, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM finance_due_payments`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("RLS leaked %d payments to school B", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wantCode("cross-tenant insert", "42501", db.WithTenant(ctx, env.App, schoolB, func(tx pgx.Tx) error {
		return insertPayment(tx, beto, "00000000-0000-0000-0000-000000000000")
	}))

	// R5: ce_app can take the row lock on one member's debt in one Due.
	if err := db.WithTenant(ctx, env.App, schoolA, func(tx pgx.Tx) error {
		var one int
		return tx.QueryRow(ctx, `SELECT 1 FROM finance_due_members WHERE tenant_id=$1 AND roster_member_id=$2 FOR UPDATE`, schoolA, beto).Scan(&one)
	}); err != nil {
		t.Fatalf("lock member debt row as ce_app: %v", err)
	}

	var count int
	if err := env.Super.QueryRow(ctx, `SELECT count(*) FROM finance_due_payments`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rejections wrote payments: count=%d err=%v", count, err)
	}

	// DownTo/UpTo pin this migration: earlier or later ones must not shift what this test rolls back.
	if _, err := p.DownTo(ctx, 27); err != nil {
		t.Fatalf("down to 27: %v", err)
	}
	var exists bool
	if err := env.Super.QueryRow(ctx, `SELECT to_regclass('public.finance_due_payments') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("finance_due_payments still exists after down: exists=%v err=%v", exists, err)
	}
	if _, err := p.UpTo(ctx, 30); err != nil {
		t.Fatalf("reapply 00030: %v", err)
	}
}
