package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

var electionTables = []string{"electoral_commissions", "electoral_commission_members", "elections", "election_positions"}

type electionSeed struct{ commission, election string }

// seedElection creates, as superuser, one row in every election table for tenant: a commission with
// one member (a roster member of tenant), a draft election and one position.
func seedElection(t *testing.T, ctx context.Context, tenant string) electionSeed {
	t.Helper()
	var s electionSeed
	var member string
	if err := env.super.QueryRow(ctx,
		`SELECT id::text FROM roster_members WHERE tenant_id = $1 ORDER BY document_number LIMIT 1`, tenant).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx,
		`INSERT INTO electoral_commissions (tenant_id) VALUES ($1) RETURNING id::text`, tenant).Scan(&s.commission); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx,
		`INSERT INTO electoral_commission_members (tenant_id, commission_id, roster_member_id) VALUES ($1, $2, $3)`,
		tenant, s.commission, member); err != nil {
		t.Fatal(err)
	}
	if err := env.super.QueryRow(ctx, `
		INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at)
		VALUES ($1, $2, 'Elección 2027', 'secret', '2027-03-01T00:00:00-03:00', '2027-03-06T00:00:00-03:00',
		        '2027-03-10T08:00:00-03:00', '2027-03-10T18:00:00-03:00') RETURNING id::text`,
		tenant, s.commission).Scan(&s.election); err != nil {
		t.Fatal(err)
	}
	if _, err := env.super.Exec(ctx,
		`INSERT INTO election_positions (tenant_id, election_id, position_order, name) VALUES ($1, $2, 1, 'Presidente')`,
		tenant, s.election); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = env.super.Exec(bg, `DELETE FROM election_positions WHERE election_id = $1`, s.election)
		_, _ = env.super.Exec(bg, `DELETE FROM elections WHERE id = $1`, s.election)
		_, _ = env.super.Exec(bg, `DELETE FROM electoral_commission_members WHERE commission_id = $1`, s.commission)
		_, _ = env.super.Exec(bg, `DELETE FROM electoral_commissions WHERE id = $1`, s.commission)
	})
	return s
}

// superCounts counts each election table's rows of tenant as superuser, bypassing RLS.
func superCounts(t *testing.T, ctx context.Context, tenant string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range electionTables {
		var n int
		if err := env.super.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[table] = n
	}
	return out
}

func TestElectionTablesForceRowLevelSecurity(t *testing.T) {
	ctx := integration(t)
	for _, table := range electionTables {
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

func TestElectionTablesFailClosedWithoutTenant(t *testing.T) {
	ctx := integration(t)
	seedElection(t, ctx, env.tenantA)
	for _, table := range electionTables {
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

func TestElectionTablesIsolateTenants(t *testing.T) {
	ctx := integration(t)
	a := seedElection(t, ctx, env.tenantA)
	before := superCounts(t, ctx, env.tenantA)

	t.Run("reads", func(t *testing.T) {
		for _, table := range electionTables {
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
			`UPDATE electoral_commissions SET ended_at = now() WHERE tenant_id = $1`,
			`DELETE FROM electoral_commission_members WHERE tenant_id = $1`,
			`UPDATE elections SET name = 'Hackeada' WHERE tenant_id = $1`,
			`DELETE FROM election_positions WHERE tenant_id = $1`,
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
		var memberA string
		if err := env.super.QueryRow(ctx,
			`SELECT id::text FROM roster_members WHERE tenant_id = $1 ORDER BY document_number DESC LIMIT 1`, env.tenantA).Scan(&memberA); err != nil {
			t.Fatal(err)
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO electoral_commissions (tenant_id) VALUES ($1)`, []any{env.tenantA}},
			{`INSERT INTO electoral_commission_members (tenant_id, commission_id, roster_member_id) VALUES ($1, $2, $3)`, []any{env.tenantA, a.commission, memberA}},
			{`INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at)
			  VALUES ($1, $2, 'Intrusa', 'secret', '2028-03-01', '2028-03-02', '2028-03-03', '2028-03-04')`, []any{env.tenantA, a.commission}},
			{`INSERT INTO election_positions (tenant_id, election_id, position_order, name) VALUES ($1, $2, 9, 'Intruso')`, []any{env.tenantA, a.election}},
		} {
			err := db.WithTenant(ctx, env.app, env.tenantB, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, q.sql, q.args...)
				return err
			})
			wantRLSError(t, err)
		}
	})

	after := superCounts(t, ctx, env.tenantA)
	for table, n := range before {
		if after[table] != n {
			t.Fatalf("%s: tenant A rows %d, want %d", table, after[table], n)
		}
	}
}

// Foreign-key checks bypass RLS: only the composite (tenant_id, …) keys stop a row of tenant B from
// pointing at a roster member, commission or election of tenant A.
func TestElectionForeignKeysStayInsideTheTenant(t *testing.T) {
	ctx := integration(t)
	a := seedElection(t, ctx, env.tenantA)
	b := seedElection(t, ctx, env.tenantB)
	var memberA string
	if err := env.super.QueryRow(ctx,
		`SELECT id::text FROM roster_members WHERE tenant_id = $1 ORDER BY document_number DESC LIMIT 1`, env.tenantA).Scan(&memberA); err != nil {
		t.Fatal(err)
	}
	before := superCounts(t, ctx, env.tenantB)
	for _, q := range []struct {
		name string
		sql  string
		args []any
	}{
		{"member from another school", `INSERT INTO electoral_commission_members (tenant_id, commission_id, roster_member_id) VALUES ($1, $2, $3)`,
			[]any{env.tenantB, b.commission, memberA}},
		// Annulled, so the one-in-progress index (tenant B already has a draft) does not answer first.
		{"commission from another school", `INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at, annulled_at, annulment_reason)
			VALUES ($1, $2, 'Cruzada', 'secret', '2028-03-01', '2028-03-02', '2028-03-03', '2028-03-04', now(), 'Prueba')`, []any{env.tenantB, a.commission}},
		{"election from another school", `INSERT INTO election_positions (tenant_id, election_id, position_order, name) VALUES ($1, $2, 9, 'Cruzado')`,
			[]any{env.tenantB, a.election}},
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
	after := superCounts(t, ctx, env.tenantB)
	for table, n := range before {
		if after[table] != n {
			t.Fatalf("%s: tenant B rows %d, want %d", table, after[table], n)
		}
	}
}

func TestElectionsAcceptOnlySecretModeAndOrderedDates(t *testing.T) {
	ctx := integration(t)
	a := seedElection(t, ctx, env.tenantA)
	for _, tc := range []struct{ name, mode, subStart, subEnd, start, end string }{
		{"nominal mode", "nominal", "2028-03-01", "2028-03-02", "2028-03-03", "2028-03-04"},
		{"end equals start", "secret", "2028-03-01", "2028-03-02", "2028-03-03", "2028-03-03"},
		{"submission ends after start", "secret", "2028-03-01", "2028-03-04", "2028-03-03", "2028-03-05"},
		{"empty submission period", "secret", "2028-03-02", "2028-03-02", "2028-03-03", "2028-03-04"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Done as superuser so neither RLS nor the in-progress index decide: only the CHECKs.
			_, err := env.super.Exec(ctx, `
				INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at, annulled_at, annulment_reason)
				VALUES ($1, $2, 'Inválida', $3, $4, $5, $6, $7, now(), 'Prueba')`,
				env.tenantA, a.commission, tc.mode, tc.subStart, tc.subEnd, tc.start, tc.end)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("err = %v, want check_violation (23514)", err)
			}
		})
	}
}
