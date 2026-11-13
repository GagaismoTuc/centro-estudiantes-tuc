package db_test

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

func memberID(t *testing.T, ctx context.Context, tenant, doc string) string {
	t.Helper()
	var id string
	if err := env.super.QueryRow(ctx,
		`SELECT id::text FROM roster_members WHERE tenant_id = $1 AND document_number = $2`, tenant, doc).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

const insertCode = `INSERT INTO access_codes (tenant_id, roster_member_id, code_hash, created_at, expires_at)
	VALUES ($1, $2, $3, now(), now() + interval '30 days')`

func randomHash() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}

func wantPgCode(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("err = %v, want SQLSTATE %s", err, code)
	}
}

func TestAccessCodesAreTenantIsolated(t *testing.T) {
	ctx := integration(t)
	ana := memberID(t, ctx, env.tenantA, "111")
	beto := memberID(t, ctx, env.tenantB, "221")
	if _, err := env.super.Exec(ctx, insertCode, env.tenantA, ana, randomHash()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = env.super.Exec(context.Background(), `DELETE FROM access_codes`) })
	count := func(tenant string) (n int, err error) {
		run := func(tx pgx.Tx) error { return tx.QueryRow(ctx, `SELECT count(*) FROM access_codes`).Scan(&n) }
		if tenant == "" {
			return n, pgx.BeginFunc(ctx, env.app, run)
		}
		return n, db.WithTenant(ctx, env.app, tenant, run)
	}

	for _, tc := range []struct {
		name, tenant string
		want         int
	}{{"own tenant", env.tenantA, 1}, {"other tenant", env.tenantB, 0}, {"no tenant", "", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			if n, err := count(tc.tenant); err != nil || n != tc.want {
				t.Fatalf("count = %d, %v; want %d", n, err, tc.want)
			}
		})
	}
	t.Run("code for another tenant's member is rejected by the composite key", func(t *testing.T) {
		err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, insertCode, env.tenantA, beto, randomHash())
			return err
		})
		wantPgCode(t, err, "23503")
	})
	t.Run("row for another tenant is rejected by the policy", func(t *testing.T) {
		err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, insertCode, env.tenantB, beto, randomHash())
			return err
		})
		wantRLSError(t, err)
	})
	t.Run("second active code for a member is rejected", func(t *testing.T) {
		err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, insertCode, env.tenantA, ana, randomHash())
			return err
		})
		wantPgCode(t, err, "23505")
	})
	t.Run("the app role cannot delete codes", func(t *testing.T) {
		err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM access_codes`)
			return err
		})
		wantRLSError(t, err) // 42501 permission denied
		if n, _ := count(env.tenantA); n != 1 {
			t.Fatalf("count after delete = %d, want 1", n)
		}
	})
}
