package db_test

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

// env is shared by every integration test: one container per `go test` run.
var env struct {
	setupErr  error
	superDSN  string
	super     *pgxpool.Pool // superuser: only for seeding and verifying data is unchanged
	app       *pgxpool.Pool // ce_app_test (member of ce_app): the pool under test
	appConfig *pgxpool.Config
	tenantA   string
	tenantB   string
}

type member struct{ Name, Doc string }

var (
	seedA = []member{{"Ana Acosta", "111"}, {"Alberto Avila", "112"}}
	seedB = []member{{"Beto Bravo", "221"}}
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("ce"), postgres.WithUsername("postgres"), postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies())
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres container:", err)
		os.Exit(1)
	}
	env.setupErr = setup(ctx, ctr)
	code := m.Run()
	if env.app != nil {
		env.app.Close()
	}
	if env.super != nil {
		env.super.Close()
	}
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

func setup(ctx context.Context, ctr *postgres.PostgresContainer) error {
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return err
	}
	env.superDSN = dsn
	if err := db.Migrate(ctx, dsn); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if env.super, err = pgxpool.New(ctx, dsn); err != nil {
		return err
	}
	for _, t := range []struct {
		id   *string
		name string
		rows []member
	}{{&env.tenantA, "Escuela A", seedA}, {&env.tenantB, "Escuela B", seedB}} {
		err := env.super.QueryRow(ctx,
			`INSERT INTO organizations (kind, name) VALUES ('school', $1) RETURNING id::text`, t.name).Scan(t.id)
		if err != nil {
			return err
		}
		for _, r := range t.rows {
			if _, err := env.super.Exec(ctx,
				`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, $2, $3)`,
				*t.id, r.Name, r.Doc); err != nil {
				return err
			}
		}
	}
	if _, err := env.super.Exec(ctx, `CREATE ROLE ce_app_test LOGIN PASSWORD 'ce_app_test' IN ROLE ce_app`); err != nil {
		return err
	}
	if env.appConfig, err = pgxpool.ParseConfig(dsn); err != nil {
		return err
	}
	env.appConfig.ConnConfig.User = "ce_app_test"
	env.appConfig.ConnConfig.Password = "ce_app_test"
	env.app, err = pgxpool.NewWithConfig(ctx, env.appConfig.Copy())
	return err
}

func integration(t *testing.T) context.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker; skipped with -short")
	}
	if env.setupErr != nil {
		t.Fatalf("setup: %v", env.setupErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func listMembers(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, where string, args ...any) ([]member, error) {
	rows, err := q.Query(ctx, `SELECT full_name, document_number FROM roster_members `+where+` ORDER BY document_number`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (member, error) {
		var m member
		return m, r.Scan(&m.Name, &m.Doc)
	})
}

// superMembers reads a tenant's rows as superuser, bypassing RLS, to verify data is unchanged.
func superMembers(t *testing.T, ctx context.Context, tenant string) []member {
	t.Helper()
	got, err := listMembers(ctx, env.super, `WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("superuser read: %v", err)
	}
	return got
}

func wantRLSError(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("err = %v, want PostgreSQL insufficient_privilege (42501)", err)
	}
}

func TestTenantReadsOnlyOwnRows(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name   string
		tenant string
		want   []member
	}{{"tenant A", env.tenantA, seedA}, {"tenant B", env.tenantB, seedB}} {
		t.Run(tc.name, func(t *testing.T) {
			var got []member
			err := db.WithTenant(ctx, env.app, tc.tenant, func(tx pgx.Tx) error {
				var err error
				got, err = listMembers(ctx, tx, "") // no WHERE: the database must filter
				return err
			})
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("rows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNoTenantReadsZeroRowsWithoutError(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name  string
		setup string
	}{
		{"setting never set", ""},
		{"setting is empty string", `SELECT set_config('app.tenant_id', '', true)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n int
			err := pgx.BeginFunc(ctx, env.app, func(tx pgx.Tx) error {
				if tc.setup != "" {
					if _, err := tx.Exec(ctx, tc.setup); err != nil {
						return err
					}
				}
				return tx.QueryRow(ctx, `SELECT count(*) FROM roster_members`).Scan(&n)
			})
			if err != nil {
				t.Fatalf("query without tenant: %v, want no error", err)
			}
			if n != 0 {
				t.Fatalf("count = %d, want 0", n)
			}
		})
	}
}

func TestInsertForOtherTenantIsRejected(t *testing.T) {
	ctx := integration(t)
	before := superMembers(t, ctx, env.tenantB)
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, 'Intruso', '999')`, env.tenantB)
		return err
	})
	wantRLSError(t, err)
	if after := superMembers(t, ctx, env.tenantB); !slices.Equal(after, before) {
		t.Fatalf("tenant B rows changed: %v, want %v", after, before)
	}
}

func TestUpdateAndDeleteOfOtherTenantAffectNothing(t *testing.T) {
	ctx := integration(t)
	before := superMembers(t, ctx, env.tenantB)
	var updated, deleted int64
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE roster_members SET full_name = 'Hackeado' WHERE tenant_id = $1`, env.tenantB)
		if err != nil {
			return err
		}
		updated = tag.RowsAffected()
		tag, err = tx.Exec(ctx, `DELETE FROM roster_members WHERE tenant_id = $1`, env.tenantB)
		deleted = tag.RowsAffected()
		return err
	})
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	if updated != 0 || deleted != 0 {
		t.Fatalf("rows affected: update=%d delete=%d, want 0 and 0", updated, deleted)
	}
	if after := superMembers(t, ctx, env.tenantB); !slices.Equal(after, before) {
		t.Fatalf("tenant B rows changed: %v, want %v", after, before)
	}
}

func TestTenantSettingDoesNotLeakToNextTransaction(t *testing.T) {
	ctx := integration(t)
	cfg := env.appConfig.Copy()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var pidA, pidPlain, countA, countPlain int
	err = db.WithTenant(ctx, pool, env.tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT pg_backend_pid(), (SELECT count(*) FROM roster_members)`).Scan(&pidA, &countA)
	})
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	var setting *string
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT pg_backend_pid(), current_setting('app.tenant_id', true), (SELECT count(*) FROM roster_members)`).
			Scan(&pidPlain, &setting, &countPlain)
	})
	if err != nil {
		t.Fatalf("plain transaction: %v", err)
	}
	if pidA != pidPlain {
		t.Fatalf("backend pid %d then %d: test did not reuse the connection", pidA, pidPlain)
	}
	if countA != len(seedA) {
		t.Fatalf("count inside WithTenant = %d, want %d", countA, len(seedA))
	}
	// After a transaction-local set_config commits, PostgreSQL reports the custom setting as "".
	if setting != nil && *setting != "" {
		t.Fatalf("app.tenant_id leaked: %q", *setting)
	}
	if countPlain != 0 {
		t.Fatalf("count in next transaction = %d, want 0", countPlain)
	}
}

func TestWithTenantRejectsNonUUID(t *testing.T) {
	for _, id := range []string{"", "not-a-uuid", "1' OR '1'='1", "0b8f2a40-0000-0000-0000-00000000000"} {
		t.Run(id, func(t *testing.T) {
			called := false
			// A nil pool proves no query runs: touching it would panic.
			err := db.WithTenant(context.Background(), nil, id, func(pgx.Tx) error { called = true; return nil })
			if !errors.Is(err, db.ErrInvalidTenantID) {
				t.Fatalf("err = %v, want ErrInvalidTenantID", err)
			}
			if called {
				t.Fatal("fn was called for an invalid tenant id")
			}
		})
	}
}

// PostgreSQL does not let a role subject to RLS switch it off: with row_security = off,
// a query that a policy would filter raises 42501 ("query would be affected by row-level
// security policy") instead of returning unfiltered rows.
func TestAppRoleCannotDisableRowSecurity(t *testing.T) {
	ctx := integration(t)
	var n int
	err := db.WithTenant(ctx, env.app, env.tenantA, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL row_security = off`); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM roster_members`).Scan(&n)
	})
	wantRLSError(t, err)
	if n != 0 {
		t.Fatalf("count = %d, want no rows read", n)
	}
}

func TestAppRolesHaveNoPrivilegeEscape(t *testing.T) {
	ctx := integration(t)
	rows, err := env.super.Query(ctx, `
		SELECT r.rolname, r.rolsuper OR r.rolbypassrls,
		       EXISTS (SELECT FROM pg_tables WHERE tablename IN ('organizations','roster_members') AND tableowner = r.rolname)
		FROM pg_roles r WHERE r.rolname IN ('ce_app', 'ce_app_test') ORDER BY r.rolname`)
	if err != nil {
		t.Fatal(err)
	}
	type role struct {
		Name         string
		Bypass, Owns bool
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[role])
	if err != nil {
		t.Fatal(err)
	}
	want := []role{{"ce_app", false, false}, {"ce_app_test", false, false}}
	if !slices.Equal(got, want) {
		t.Fatalf("roles = %+v, want %+v", got, want)
	}
}

func TestMigrateIsIdempotentAndReversible(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE migrate_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/migrate_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)

	for i := range 2 {
		if err := db.Migrate(ctx, dsn); err != nil {
			t.Fatalf("Migrate run %d: %v", i+1, err)
		}
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	tables := func() (org, roster bool) {
		t.Helper()
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT to_regclass('organizations') IS NOT NULL, to_regclass('roster_members') IS NOT NULL`).
			Scan(&org, &roster); err != nil {
			t.Fatal(err)
		}
		return
	}
	if org, roster := tables(); !org || !roster {
		t.Fatalf("after up: organizations=%v roster_members=%v, want both", org, roster)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if org, roster := tables(); org || roster {
		t.Fatalf("after down: organizations=%v roster_members=%v, want neither", org, roster)
	}
}
