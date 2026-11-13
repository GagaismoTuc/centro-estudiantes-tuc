package db_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

type membership struct{ Tenant, User, Role string }

// seedMemberships creates two users: ana belongs to tenants A and B, beto only to B.
func seedMemberships(t *testing.T, ctx context.Context) (ana, beto string) {
	t.Helper()
	for _, u := range []struct {
		id    *string
		email string
	}{{&ana, "ana-" + t.Name() + "@example.com"}, {&beto, "beto-" + t.Name() + "@example.com"}} {
		if err := env.super.QueryRow(ctx,
			`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`, u.email).Scan(u.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []membership{{env.tenantA, ana, "school_admin"}, {env.tenantB, ana, "officer"}, {env.tenantB, beto, "student"}} {
		if _, err := env.super.Exec(ctx,
			`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)`, m.Tenant, m.User, m.Role); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM memberships WHERE user_id IN ($1, $2)`, ana, beto)
		_, _ = env.super.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1, $2)`, ana, beto)
	})
	return ana, beto
}

func listMemberships(ctx context.Context, tx pgx.Tx) ([]membership, error) {
	rows, err := tx.Query(ctx, `SELECT tenant_id::text, user_id::text, role FROM memberships ORDER BY tenant_id, role`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[membership])
}

func sortMemberships(ms []membership) []membership {
	slices.SortFunc(ms, func(a, b membership) int {
		if a.Tenant != b.Tenant {
			if a.Tenant < b.Tenant {
				return -1
			}
			return 1
		}
		if a.Role < b.Role {
			return -1
		}
		return 1
	})
	return ms
}

func TestMembershipsAreTenantIsolated(t *testing.T) {
	ctx := integration(t)
	ana, beto := seedMemberships(t, ctx)
	for _, tc := range []struct {
		name   string
		tenant string
		want   []membership
	}{
		{"tenant A", env.tenantA, []membership{{env.tenantA, ana, "school_admin"}}},
		{"tenant B", env.tenantB, sortMemberships([]membership{{env.tenantB, ana, "officer"}, {env.tenantB, beto, "student"}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []membership
			err := db.WithTenant(ctx, env.app, tc.tenant, func(tx pgx.Tx) error {
				var err error
				got, err = listMemberships(ctx, tx)
				return err
			})
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("memberships = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("no tenant sees nothing", func(t *testing.T) {
		var got []membership
		err := pgx.BeginFunc(ctx, env.app, func(tx pgx.Tx) error {
			var err error
			got, err = listMemberships(ctx, tx)
			return err
		})
		if err != nil || len(got) != 0 {
			t.Fatalf("memberships = %v, %v; want none and no error", got, err)
		}
	})
	t.Run("insert into other tenant is rejected", func(t *testing.T) {
		err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'student')`, env.tenantB, ana)
			return err
		})
		wantRLSError(t, err)
	})
}

func TestWithUserSeesOnlyOwnMembershipsAcrossTenants(t *testing.T) {
	ctx := integration(t)
	ana, _ := seedMemberships(t, ctx)
	var got []membership
	err := db.WithUser(ctx, env.app, ana, func(tx pgx.Tx) error {
		var err error
		got, err = listMemberships(ctx, tx)
		return err
	})
	if err != nil {
		t.Fatalf("WithUser: %v", err)
	}
	want := sortMemberships([]membership{{env.tenantA, ana, "school_admin"}, {env.tenantB, ana, "officer"}})
	if !slices.Equal(got, want) {
		t.Fatalf("memberships = %v, want %v", got, want)
	}
	// The user policy is read-only: it never lets a write through without a tenant.
	err = db.WithUser(ctx, env.app, ana, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE memberships SET role = 'student' WHERE user_id = $1`, ana)
		if err == nil && tag.RowsAffected() != 0 {
			return errors.New("update affected rows")
		}
		return err
	})
	if err != nil {
		t.Fatalf("update through WithUser: %v, want no rows affected", err)
	}
	if err := db.WithUser(context.Background(), nil, "nope", func(pgx.Tx) error { return nil }); !errors.Is(err, db.ErrInvalidUserID) {
		t.Fatalf("WithUser(non-UUID) = %v, want ErrInvalidUserID", err)
	}
}

func TestMembershipCannotLinkRosterMemberOfOtherTenant(t *testing.T) {
	ctx := integration(t)
	ana, _ := seedMemberships(t, ctx)
	var rosterB string
	if err := env.super.QueryRow(ctx, `SELECT id::text FROM roster_members WHERE tenant_id = $1 LIMIT 1`, env.tenantB).Scan(&rosterB); err != nil {
		t.Fatal(err)
	}
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE memberships SET roster_member_id = $1 WHERE user_id = $2`, rosterB, ana)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("err = %v, want foreign_key_violation (23503)", err)
	}
}

func TestCheckAppRole(t *testing.T) {
	ctx := integration(t)
	if err := db.CheckAppRole(ctx, env.app); err != nil {
		t.Fatalf("CheckAppRole(app pool) = %v, want nil", err)
	}
	if err := db.CheckAppRole(ctx, env.super); err == nil {
		t.Fatal("CheckAppRole(superuser pool) = nil, want an error")
	}
}
