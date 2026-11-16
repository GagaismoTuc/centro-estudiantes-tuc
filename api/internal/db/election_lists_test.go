package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
)

var electionListTables = []string{"election_lists", "election_list_candidates"}

type electionListSeed struct {
	electionSeed
	repID       string
	candidateID string
	listID      string
}

// seedElectionList creates, as superuser, one election (via seedElection) plus one pending
// election_list and one election_list_candidates row for tenant.
func seedElectionList(t *testing.T, ctx context.Context, tenant string) electionListSeed {
	t.Helper()
	es := seedElection(t, ctx, tenant)
	rows, err := env.super.Query(ctx,
		`SELECT id::text FROM roster_members WHERE tenant_id = $1 ORDER BY document_number LIMIT 2`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var repID, candID string
	if len(ids) >= 2 {
		repID, candID = ids[0], ids[1]
	} else {
		repID = ids[0]
		if err := env.super.QueryRow(ctx,
			`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, 'Candidato Extra', '99999999') RETURNING id::text`,
			tenant).Scan(&candID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = env.super.Exec(context.Background(), `DELETE FROM roster_members WHERE id = $1`, candID)
		})
	}
	var listID string
	if err := env.super.QueryRow(ctx, `
		INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status)
		VALUES ($1, $2, 'Lista Seed', $3, 'pending') RETURNING id::text`,
		tenant, es.election, repID).Scan(&listID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx, `
		INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id)
		VALUES ($1, $2, $3, 1, $4)`,
		tenant, es.election, listID, candID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = env.super.Exec(bg, `DELETE FROM election_list_candidates WHERE list_id = $1`, listID)
		_, _ = env.super.Exec(bg, `DELETE FROM election_lists WHERE id = $1`, listID)
	})
	return electionListSeed{electionSeed: es, repID: repID, candidateID: candID, listID: listID}
}

func superListCounts(t *testing.T, ctx context.Context, tenant string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range electionListTables {
		var n int
		if err := env.super.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[table] = n
	}
	return out
}

func TestElectionListTablesForceRowLevelSecurity(t *testing.T) {
	ctx := integration(t)
	for _, table := range electionListTables {
		var enabled, forced bool
		if err := env.super.QueryRow(ctx,
			`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = to_regclass($1)`, table).Scan(&enabled, &forced); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if !enabled || !forced {
			t.Fatalf("%s: row security enabled=%v forced=%v, want both", table, enabled, forced)
		}
	}
}

func TestElectionListTablesFailClosedWithoutTenant(t *testing.T) {
	ctx := integration(t)
	seedElectionList(t, ctx, env.tenantA)
	for _, table := range electionListTables {
		for _, setup := range []string{"", `SELECT set_config('app.tenant_id', '', true)`} {
			var n int
			err := pgx.BeginFunc(ctx, env.app, func(tx pgx.Tx) error {
				if setup != "" {
					if _, err := tx.Exec(ctx, setup); err != nil {
						return err
					}
				}
				return tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n)
			})
			if err != nil {
				t.Fatalf("%s %q: %v, want no error", table, setup, err)
			}
			if n != 0 {
				t.Fatalf("%s %q: count = %d, want 0", table, setup, n)
			}
		}
	}
}

func TestElectionListTablesIsolateTenants(t *testing.T) {
	ctx := integration(t)
	a := seedElectionList(t, ctx, env.tenantA)
	before := superListCounts(t, ctx, env.tenantA)

	t.Run("reads", func(t *testing.T) {
		for _, table := range electionListTables {
			var own, other int
			err := db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE tenant_id = $1), count(*) FILTER (WHERE tenant_id = $2) FROM `+table,
					env.tenantB, env.tenantA).Scan(&own, &other)
			})
			if err != nil {
				t.Fatalf("%s: %v", table, err)
			}
			if other != 0 {
				t.Fatalf("%s: tenant B sees %d rows of tenant A", table, other)
			}
		}
	})

	t.Run("updates and deletes", func(t *testing.T) {
		for _, q := range []string{
			`UPDATE election_lists SET name = 'Hackeada' WHERE tenant_id = $1`,
			`DELETE FROM election_list_candidates WHERE tenant_id = $1`,
		} {
			var affected int64
			err := db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, q, env.tenantA)
				affected = tag.RowsAffected()
				return err
			})
			if err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			if affected != 0 {
				t.Fatalf("%s: affected %d rows, want 0", q, affected)
			}
		}
	})

	t.Run("inserts for another tenant", func(t *testing.T) {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status) VALUES ($1, $2, 'Intrusa', $3, 'pending')`,
				[]any{env.tenantA, a.election, a.repID}},
			{`INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id) VALUES ($1, $2, $3, 1, $4)`,
				[]any{env.tenantA, a.election, a.listID, a.repID}},
		} {
			err := db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, q.sql, q.args...)
				return err
			})
			wantRLSError(t, err)
		}
	})

	after := superListCounts(t, ctx, env.tenantA)
	for table, n := range before {
		if after[table] != n {
			t.Fatalf("%s: tenant A rows %d, want %d", table, after[table], n)
		}
	}
}

func TestElectionListForeignKeysStayInsideTheTenant(t *testing.T) {
	ctx := integration(t)
	a := seedElectionList(t, ctx, env.tenantA)
	b := seedElectionList(t, ctx, env.tenantB)
	var extraB string
	if err := env.super.QueryRow(ctx,
		`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, 'Otro B', '88888888') RETURNING id::text`,
		env.tenantB).Scan(&extraB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(), `DELETE FROM roster_members WHERE id = $1`, extraB)
	})
	if _, err := env.super.Exec(ctx,
		`INSERT INTO election_positions (tenant_id, election_id, position_order, name) VALUES ($1, $2, 2, 'Vicepresidente')`,
		env.tenantB, b.election); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.super.Exec(context.Background(),
			`DELETE FROM election_positions WHERE election_id = $1 AND position_order = 2`, b.election)
	})

	before := superListCounts(t, ctx, env.tenantB)
	for _, q := range []struct {
		name string
		sql  string
		args []any
	}{
		{"election from another school", `INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status) VALUES ($1, $2, 'Cruzada', $3, 'pending')`,
			[]any{env.tenantB, a.election, b.repID}},
		{"representative from another school", `INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status) VALUES ($1, $2, 'Cruzada', $3, 'pending')`,
			[]any{env.tenantB, b.election, a.repID}},
		{"list from another school", `INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id) VALUES ($1, $2, $3, 2, $4)`,
			[]any{env.tenantB, b.election, a.listID, extraB}},
		{"candidate from another school", `INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id) VALUES ($1, $2, $3, 2, $4)`,
			[]any{env.tenantB, b.election, b.listID, a.repID}},
	} {
		t.Run(q.name, func(t *testing.T) {
			err := db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, q.sql, q.args...)
				return err
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
				t.Fatalf("err = %v, want foreign_key_violation (23503)", err)
			}
		})
	}
	after := superListCounts(t, ctx, env.tenantB)
	for table, n := range before {
		if after[table] != n {
			t.Fatalf("%s: tenant B rows %d, want %d", table, after[table], n)
		}
	}
}

func TestElectionListConstraintsAndCandidateUniqueness(t *testing.T) {
	ctx := integration(t)
	s := seedElectionList(t, ctx, env.tenantA)
	// Add a second position and two more roster members for uniqueness checks.
	if _, err := env.super.Exec(ctx,
		`INSERT INTO election_positions (tenant_id, election_id, position_order, name) VALUES ($1, $2, 2, 'Vicepresidente')`,
		env.tenantA, s.election); err != nil {
		t.Fatal(err)
	}
	var m3, m4 string
	if err := env.super.QueryRow(ctx,
		`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, 'Tercero', '77777771') RETURNING id::text`,
		env.tenantA).Scan(&m3); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx,
		`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, 'Cuarto', '77777772') RETURNING id::text`,
		env.tenantA).Scan(&m4); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = env.super.Exec(bg, `DELETE FROM election_list_candidates WHERE tenant_id = $1 AND election_id = $2`, env.tenantA, s.election)
		_, _ = env.super.Exec(bg, `DELETE FROM election_lists WHERE tenant_id = $1 AND election_id = $2`, env.tenantA, s.election)
		_, _ = env.super.Exec(bg, `DELETE FROM election_positions WHERE election_id = $1 AND position_order = 2`, s.election)
		_, _ = env.super.Exec(bg, `DELETE FROM roster_members WHERE id IN ($1, $2)`, m3, m4)
	})

	// CHECK constraints on election_lists.
	for _, tc := range []struct {
		name, listName, status string
		reason                 *string
		isActive               bool
	}{
		{"blank name", "   ", "pending", nil, true},
		{"81 char name", strings.Repeat("a", 81), "pending", nil, true},
		{"rejected without reason", "Otra", "rejected", nil, false},
		{"approved with reason", "Otra", "approved", new("no"), true},
		{"rejected with 501 char reason", "Otra", "rejected", new(strings.Repeat("x", 501)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.super.Exec(ctx, `
				INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status, is_active, rejection_reason)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				env.tenantA, s.election, tc.listName, s.repID, tc.status, tc.isActive, tc.reason)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("err = %v, want check_violation (23514)", err)
			}
		})
	}

	// Duplicate active list name (case-insensitive) is rejected.
	_, err := env.super.Exec(ctx, `
		INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status)
		VALUES ($1, $2, 'lista seed', $3, 'pending')`, env.tenantA, s.election, s.repID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate list name: err = %v, want unique_violation (23505)", err)
	}

	// Second pending list cannot reuse s.candidateID.
	var list2 string
	if err := env.super.QueryRow(ctx, `
		INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status)
		VALUES ($1, $2, 'Lista Dos', $3, 'pending') RETURNING id::text`,
		env.tenantA, s.election, s.repID).Scan(&list2); err != nil {
		t.Fatal(err)
	}
	_, err = env.super.Exec(ctx, `
		INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id)
		VALUES ($1, $2, $3, 1, $4)`, env.tenantA, s.election, list2, s.candidateID)
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate candidate across lists: err = %v, want unique_violation (23505)", err)
	}

	// Rejecting the first list cascades is_active=false to its candidates, freeing both name and candidate.
	if _, err := env.super.Exec(ctx, `
		UPDATE election_lists SET status = 'rejected', is_active = false, rejection_reason = 'Incompleta', reviewed_at = now()
		WHERE id = $1`, s.listID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx, `
		INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id)
		VALUES ($1, $2, $3, 1, $4)`, env.tenantA, s.election, list2, s.candidateID); err != nil {
		t.Fatalf("reusing candidate of rejected list: %v", err)
	}
	// Same candidate twice on the same list (position 2) is still rejected.
	_, err = env.super.Exec(ctx, `
		INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id)
		VALUES ($1, $2, $3, 2, $4)`, env.tenantA, s.election, list2, s.candidateID)
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate candidate in same list: err = %v, want unique_violation (23505)", err)
	}
	// Reusing the rejected list's name is allowed.
	if _, err := env.super.Exec(ctx, `
		INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status)
		VALUES ($1, $2, 'lista seed', $3, 'pending')`, env.tenantA, s.election, m3); err != nil {
		t.Fatalf("reusing rejected list name: %v", err)
	}
	_ = m4
}

func TestElectionListsMigrationIsReversible(t *testing.T) {
	ctx := integration(t)
	if _, err := env.super.Exec(ctx, `CREATE DATABASE election_lists_migration_check`); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(env.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/election_lists_migration_check?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	checkTables := func(want bool) {
		t.Helper()
		var lists, cands bool
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT to_regclass('election_lists') IS NOT NULL, to_regclass('election_list_candidates') IS NOT NULL`).
			Scan(&lists, &cands); err != nil {
			t.Fatal(err)
		}
		if lists != want || cands != want {
			t.Fatalf("tables present = (%v, %v), want (%v, %v)", lists, cands, want, want)
		}
	}
	checkTables(true)
	if _, err := p.DownTo(ctx, 13); err != nil {
		t.Fatalf("down: %v", err)
	}
	checkTables(false)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	checkTables(true)
}
