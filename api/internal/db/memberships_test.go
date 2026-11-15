package db_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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

func TestMembershipAllowsDistinctRolesButRejectsDuplicates(t *testing.T) {
	ctx := integration(t)
	ana, _ := seedMemberships(t, ctx)
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'student')`, env.tenantA, ana)
		return err
	})
	if err != nil {
		t.Fatalf("adding a different role: %v", err)
	}
	err = db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'student')`, env.tenantA, ana)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("adding a duplicate role: %v, want unique violation", err)
	}
}

func TestOfficerPositionsFailClosedAndRejectOtherTenantRoster(t *testing.T) {
	ctx := integration(t)
	var memberA, memberB string
	if err := env.super.QueryRow(ctx, `SELECT id::text FROM roster_members WHERE tenant_id=$1 LIMIT 1`, env.tenantA).Scan(&memberA); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `SELECT id::text FROM roster_members WHERE tenant_id=$1 LIMIT 1`, env.tenantB).Scan(&memberB); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1,$2,'Tesorero')`, env.tenantA, memberA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM officer_positions WHERE tenant_id=$1`, env.tenantA)
	})
	var count int
	if err := env.app.QueryRow(ctx, `SELECT count(*) FROM officer_positions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("without tenant: %d, %v", count, err)
	}
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1,$2,'Presidente')`, env.tenantA, memberB)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant foreign key = %v, want 23503", err)
	}
	var forced bool
	if err := env.super.QueryRow(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='officer_positions'::regclass`).Scan(&forced); err != nil || !forced {
		t.Fatalf("forced RLS = %v, %v", forced, err)
	}
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

// A table owner is not subject to its table's RLS unless FORCE is set, and can ALTER or DROP it: a
// member of ce_app that owns (or is a member of a role that owns) a table in public is refused.
func TestCheckAppRoleRejectsTableOwners(t *testing.T) {
	ctx := integration(t)
	setup := []string{
		`CREATE ROLE t7b_owner LOGIN PASSWORD 't7b' IN ROLE ce_app`,
		`CREATE ROLE t7b_owners NOLOGIN`,
		`CREATE ROLE t7b_via_group LOGIN PASSWORD 't7b' IN ROLE ce_app, t7b_owners`,
		`CREATE TABLE public.t7b_owned (id int)`,
		`ALTER TABLE public.t7b_owned OWNER TO t7b_owner`,
		`CREATE TABLE public.t7b_group_owned (id int)`,
		`ALTER TABLE public.t7b_group_owned OWNER TO t7b_owners`,
	}
	t.Cleanup(func() {
		for _, q := range []string{`DROP TABLE IF EXISTS public.t7b_owned`, `DROP TABLE IF EXISTS public.t7b_group_owned`,
			`DROP ROLE IF EXISTS t7b_owner`, `DROP ROLE IF EXISTS t7b_via_group`, `DROP ROLE IF EXISTS t7b_owners`} {
			_, _ = env.super.Exec(context.Background(), q)
		}
	})
	for _, q := range setup {
		if _, err := env.super.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{"t7b_owner", "t7b_via_group"} {
		cfg := env.appConfig.Copy()
		cfg.ConnConfig.User, cfg.ConnConfig.Password = role, "t7b"
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		err = db.CheckAppRole(ctx, pool)
		pool.Close()
		want := fmt.Sprintf(`db: role %q must be a member of ce_app without SUPERUSER or BYPASSRLS and own no table`, role)
		if err == nil || err.Error() != want {
			t.Fatalf("CheckAppRole(%s) = %v, want %q", role, err, want)
		}
	}
}

// Redeem no longer switches a session's tenant in place (it rotates the session), so the app role
// must not keep UPDATE on sessions.tenant_id.
func TestAppRoleCannotUpdateSessionTenant(t *testing.T) {
	ctx := integration(t)
	_, err := env.app.Exec(ctx, `UPDATE sessions SET tenant_id = NULL WHERE false`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("err = %v, want SQLSTATE 42501 (permission denied)", err)
	}
}
