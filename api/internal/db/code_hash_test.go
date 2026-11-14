package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// Holding a code reveals that one row and nothing else, before any tenant is known.
func TestCodeHashSeesOnlyItsOwnCode(t *testing.T) {
	ctx := integration(t)
	ana, alberto := memberID(t, ctx, env.tenantA, "111"), memberID(t, ctx, env.tenantA, "112")
	beto := memberID(t, ctx, env.tenantB, "221")
	mine, sameTenant, otherTenant := randomHash(), randomHash(), randomHash()
	for _, c := range []struct {
		tenant, member string
		hash           []byte
	}{{env.tenantA, ana, mine}, {env.tenantA, alberto, sameTenant}, {env.tenantB, beto, otherTenant}} {
		if _, err := env.super.Exec(ctx, insertCode, c.tenant, c.member, c.hash); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = env.super.Exec(context.Background(), `DELETE FROM access_codes`) })

	visible := func(tx pgx.Tx) (hashes [][]byte, err error) {
		rows, err := tx.Query(ctx, `SELECT code_hash FROM access_codes`) // no WHERE: RLS must filter
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowTo[[]byte])
	}

	t.Run("the holder sees exactly that row and can mark it used", func(t *testing.T) {
		var got [][]byte
		var updated int64
		err := db.WithCodeHash(ctx, env.app, mine, func(tx pgx.Tx) error {
			var err error
			if got, err = visible(tx); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `UPDATE access_codes SET used_at = now()`)
			updated = tag.RowsAffected()
			return err
		})
		if err != nil || len(got) != 1 || string(got[0]) != string(mine) || updated != 1 {
			t.Fatalf("rows = %d, updated = %d, err = %v; want only the held code", len(got), updated, err)
		}
		var used int
		if err := env.super.QueryRow(ctx, `SELECT count(*) FROM access_codes WHERE used_at IS NOT NULL`).Scan(&used); err != nil || used != 1 {
			t.Fatalf("used codes = %d (%v), want 1", used, err)
		}
	})
	t.Run("unset or empty setting sees nothing", func(t *testing.T) {
		for _, setup := range []string{"", `SELECT set_config('app.code_hash', '', true)`} {
			var got [][]byte
			err := pgx.BeginFunc(ctx, env.app, func(tx pgx.Tx) error {
				if setup != "" {
					if _, err := tx.Exec(ctx, setup); err != nil {
						return err
					}
				}
				var err error
				got, err = visible(tx)
				return err
			})
			if err != nil || len(got) != 0 {
				t.Fatalf("setup %q: rows = %d, err = %v; want none", setup, len(got), err)
			}
		}
	})
	t.Run("a code hash cannot insert codes", func(t *testing.T) {
		fresh := randomHash()
		err := db.WithCodeHash(ctx, env.app, fresh, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, insertCode, env.tenantA, alberto, fresh)
			return err
		})
		wantRLSError(t, err)
	})
	t.Run("SetTenant inside the transaction opens that tenant", func(t *testing.T) {
		var n int
		err := db.WithCodeHash(ctx, env.app, otherTenant, func(tx pgx.Tx) error {
			if err := db.SetTenant(ctx, tx, env.tenantB); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT count(*) FROM roster_members`).Scan(&n)
		})
		if err != nil || n != len(seedB) {
			t.Fatalf("roster rows = %d, err = %v; want %d", n, err, len(seedB))
		}
	})
}

func TestCodeHashHelpersRejectBadInput(t *testing.T) {
	for _, h := range [][]byte{nil, make([]byte, 31), make([]byte, 33)} {
		// A nil pool proves no query runs: touching it would panic.
		err := db.WithCodeHash(context.Background(), nil, h, func(pgx.Tx) error { t.Fatal("fn called"); return nil })
		if !errors.Is(err, db.ErrInvalidCodeHash) {
			t.Fatalf("len %d: err = %v, want ErrInvalidCodeHash", len(h), err)
		}
	}
	if err := db.SetTenant(context.Background(), nil, "1' OR '1'='1"); !errors.Is(err, db.ErrInvalidTenantID) {
		t.Fatalf("SetTenant err = %v, want ErrInvalidTenantID", err)
	}
}

func TestCodeHashCannotMoveRowToOtherTenant(t *testing.T) {
	ctx := integration(t)
	ana, beto := memberID(t, ctx, env.tenantA, "111"), memberID(t, ctx, env.tenantB, "221")
	h := randomHash()
	if _, err := env.super.Exec(ctx, insertCode, env.tenantA, ana, h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM access_codes WHERE code_hash = $1`, h)
	})
	err := db.WithCodeHash(ctx, env.app, h, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE access_codes SET tenant_id = $1, roster_member_id = $2 WHERE code_hash = $3`, env.tenantB, beto, h)
		return err
	})
	if err == nil {
		t.Fatal("code holder moved its code to another tenant")
	}
	var tenant string
	if err := env.super.QueryRow(ctx, `SELECT tenant_id::text FROM access_codes WHERE code_hash = $1`, h).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if tenant != env.tenantA {
		t.Fatalf("tenant_id = %s, want unchanged %s", tenant, env.tenantA)
	}
}
