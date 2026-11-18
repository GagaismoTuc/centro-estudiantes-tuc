package db_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

var voteTables = []string{
	"election_electorates", "election_electorate_members", "election_voters",
	"election_ballot_pending", "election_ballots",
}

// TestElectionVotesMigration00034 covers the storage of issue #56: S2 (one electorate snapshot per
// election), S10 (box and registry without a link to the voter; ce_app cannot read pending ballots
// nor the box's id or system columns) and S15 (tenant_id, forced RLS failing closed, composite FKs).
func TestElectionVotesMigration00034(t *testing.T) {
	if _, err := migrations.FS.ReadFile("00034_election_votes.sql"); err != nil {
		t.Fatalf("election votes migration 00034 is missing: %v", err)
	}
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	defer stop()
	if err != nil {
		t.Fatal(err)
	}
	su := env.Super

	t.Run("S15 RLS enabled and forced on every table", func(t *testing.T) {
		for _, tbl := range voteTables {
			var rls, forced bool
			if err := su.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = to_regclass('public.'||$1)`, tbl).Scan(&rls, &forced); err != nil {
				t.Fatalf("%s missing: %v", tbl, err)
			}
			if !rls || !forced {
				t.Fatalf("%s: RLS enabled=%v forced=%v, want both true", tbl, rls, forced)
			}
		}
	})

	t.Run("S10 columns hold no voter, session or time", func(t *testing.T) {
		want := map[string][]string{
			"election_electorates":        {"election_id", "size", "tenant_id"},
			"election_electorate_members": {"election_id", "roster_member_id", "tenant_id"},
			"election_voters":             {"election_id", "roster_member_id", "tenant_id"},
			"election_ballot_pending":     {"election_id", "list_id", "tenant_id"},
			"election_ballots":            {"election_id", "id", "list_id", "tenant_id"},
		}
		for tbl, cols := range want {
			rows, err := su.Query(ctx, `SELECT column_name::text FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 ORDER BY 1`, tbl)
			if err != nil {
				t.Fatal(err)
			}
			got, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, cols) {
				t.Fatalf("%s columns = %v, want %v", tbl, got, cols)
			}
		}
		var def string
		if err := su.QueryRow(ctx, `SELECT pg_get_expr(adbin, adrelid) FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
			WHERE d.adrelid = 'public.election_ballots'::regclass AND a.attname = 'id'`).Scan(&def); err != nil {
			t.Fatal(err)
		}
		if def != "gen_random_uuid()" {
			t.Fatalf("election_ballots.id default = %q, want gen_random_uuid()", def)
		}
	})

	t.Run("S10 ce_app privileges", func(t *testing.T) {
		for _, tc := range []struct {
			tbl  string
			want map[string]bool
		}{
			{"election_electorates", map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": false, "DELETE": false, "TRUNCATE": false}},
			{"election_electorate_members", map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": false, "DELETE": false, "TRUNCATE": false}},
			// Column grants only: the registry's xmin would give the voting order (S10). Votes are written
			// through election_ballot_cast only, so a voter row never exists without its Ballot.
			{"election_voters", map[string]bool{"SELECT": false, "INSERT": false, "UPDATE": false, "DELETE": false, "TRUNCATE": false}},
			{"election_ballot_pending", map[string]bool{"SELECT": false, "INSERT": false, "UPDATE": false, "DELETE": false, "TRUNCATE": false}},
			{"election_ballots", map[string]bool{"SELECT": false, "INSERT": false, "UPDATE": false, "DELETE": false, "TRUNCATE": false}},
		} {
			for priv, want := range tc.want {
				var has bool
				if err := su.QueryRow(ctx, `SELECT has_table_privilege('ce_app', 'public.'||$1, $2)`, tc.tbl, priv).Scan(&has); err != nil {
					t.Fatal(err)
				}
				if has != want {
					t.Fatalf("ce_app %s on %s = %v, want %v", priv, tc.tbl, has, want)
				}
			}
		}
		var anyPending bool
		if err := su.QueryRow(ctx, `SELECT has_any_column_privilege('ce_app', 'public.election_ballot_pending', 'SELECT,INSERT,UPDATE,REFERENCES')`).Scan(&anyPending); err != nil {
			t.Fatal(err)
		}
		if anyPending {
			t.Fatal("ce_app has a column privilege on election_ballot_pending")
		}
		for _, cc := range []struct {
			tbl, col string
			want     bool
		}{
			{"election_ballots", "tenant_id", true}, {"election_ballots", "election_id", true}, {"election_ballots", "list_id", true}, {"election_ballots", "id", false},
			{"election_voters", "tenant_id", true}, {"election_voters", "election_id", true}, {"election_voters", "roster_member_id", true},
		} {
			var has bool
			if err := su.QueryRow(ctx, `SELECT has_column_privilege('ce_app', 'public.'||$1, $2, 'SELECT')`, cc.tbl, cc.col).Scan(&has); err != nil {
				t.Fatal(err)
			}
			if has != cc.want {
				t.Fatalf("ce_app SELECT (%s) on %s = %v, want %v", cc.col, cc.tbl, has, cc.want)
			}
		}
		var anyVoterWrite bool
		if err := su.QueryRow(ctx, `SELECT has_any_column_privilege('ce_app', 'public.election_voters', 'INSERT,UPDATE')`).Scan(&anyVoterWrite); err != nil {
			t.Fatal(err)
		}
		if anyVoterWrite {
			t.Fatal("ce_app may write election_voters directly")
		}
		for _, fn := range []string{"public.election_ballot_cast(uuid, uuid, uuid)", "public.election_ballots_flush(uuid)"} {
			var app, public, definer bool
			var config []string
			if err := su.QueryRow(ctx, `SELECT has_function_privilege('ce_app', $1::regprocedure, 'EXECUTE'),
				       has_function_privilege('public', $1::regprocedure, 'EXECUTE'), p.prosecdef, coalesce(p.proconfig, '{}')
				FROM pg_proc p WHERE p.oid = $1::regprocedure`, fn).Scan(&app, &public, &definer, &config); err != nil {
				t.Fatalf("%s: %v", fn, err)
			}
			if !app || public || !definer || !slices.ContainsFunc(config, func(s string) bool { return len(s) > 12 && s[:12] == "search_path=" }) {
				t.Fatalf("%s: ce_app=%v public=%v definer=%v config=%v, want true/false/true with search_path", fn, app, public, definer, config)
			}
		}
	})

	// Seed two schools, each with an election, an approved list and two roster members.
	type fixture struct{ tenant, election, list, ana, beto string }
	seed := func(name, doc string) fixture {
		t.Helper()
		var f fixture
		if err := su.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school',$1) RETURNING id::text`, name).Scan(&f.tenant); err != nil {
			t.Fatal(err)
		}
		if err := su.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Ana Ficticia',$2) RETURNING id::text`, f.tenant, doc+"1").Scan(&f.ana); err != nil {
			t.Fatal(err)
		}
		if err := su.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Beto Ficticio',$2) RETURNING id::text`, f.tenant, doc+"2").Scan(&f.beto); err != nil {
			t.Fatal(err)
		}
		var commission string
		if err := su.QueryRow(ctx, `INSERT INTO electoral_commissions(tenant_id) VALUES ($1) RETURNING id::text`, f.tenant).Scan(&commission); err != nil {
			t.Fatal(err)
		}
		if err := su.QueryRow(ctx, `INSERT INTO elections(tenant_id,commission_id,name,mode,submission_starts_at,submission_ends_at,starts_at,ends_at)
			VALUES ($1,$2,'Elección','secret','2027-03-01','2027-03-06','2027-03-10 08:00','2027-03-10 18:00') RETURNING id::text`, f.tenant, commission).Scan(&f.election); err != nil {
			t.Fatal(err)
		}
		if err := su.QueryRow(ctx, `INSERT INTO election_lists(tenant_id,election_id,name,representative_id,status) VALUES ($1,$2,'Lista Azul',$3,'approved') RETURNING id::text`,
			f.tenant, f.election, f.ana).Scan(&f.list); err != nil {
			t.Fatal(err)
		}
		return f
	}
	a := seed("Escuela A", "4000")
	b := seed("Escuela B", "5000")
	asApp := func(tenant string, fn func(pgx.Tx) error) error { return db.WithTenant(ctx, env.App, tenant, fn) }
	exec := func(tenant, stmt string, args ...any) error {
		return asApp(tenant, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, stmt, args...); return err })
	}
	code := func(err error) string {
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			return pe.Code
		}
		return ""
	}
	snapshot := func(f fixture) error {
		return asApp(f.tenant, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO election_electorates(tenant_id,election_id,size) VALUES ($1,$2,2)`, f.tenant, f.election); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO election_electorate_members(tenant_id,election_id,roster_member_id) VALUES ($1,$2,$3),($1,$2,$4)`, f.tenant, f.election, f.ana, f.beto)
			return err
		})
	}
	if err := snapshot(a); err != nil {
		t.Fatalf("snapshot A as ce_app: %v", err)
	}
	if err := snapshot(b); err != nil {
		t.Fatalf("snapshot B as ce_app: %v", err)
	}

	t.Run("S2 one snapshot per election", func(t *testing.T) {
		if err := exec(a.tenant, `INSERT INTO election_electorates(tenant_id,election_id,size) VALUES ($1,$2,5)`, a.tenant, a.election); code(err) != "23505" {
			t.Fatalf("second snapshot marker: got %v, want 23505", err)
		}
		if err := exec(a.tenant, `INSERT INTO election_electorate_members(tenant_id,election_id,roster_member_id) VALUES ($1,$2,$3)`, a.tenant, a.election, a.ana); code(err) != "23505" {
			t.Fatalf("repeated electorate member: got %v, want 23505", err)
		}
	})

	t.Run("S15 composite keys and RLS isolate schools", func(t *testing.T) {
		for _, tc := range []struct {
			name, asTenant, stmt, code string
			args                       []any
		}{
			{"voter outside the electorate snapshot of its school", a.tenant, `SELECT election_ballot_cast($1, $2, NULL)`, "23503", []any{a.election, b.ana}},
			{"electorate member of another school", a.tenant, `INSERT INTO election_electorate_members(tenant_id,election_id,roster_member_id) VALUES ($1,$2,$3)`, "23503", []any{a.tenant, a.election, b.beto}},
			{"electorate for another school's election", a.tenant, `INSERT INTO election_electorates(tenant_id,election_id,size) VALUES ($1,$2,0)`, "23503", []any{a.tenant, b.election}},
			{"voter row of another tenant", a.tenant, `INSERT INTO election_voters(tenant_id,election_id,roster_member_id) VALUES ($1,$2,$3)`, "42501", []any{b.tenant, b.election, b.ana}},
			{"cast into another school's election", a.tenant, `SELECT election_ballot_cast($1, $2, NULL)`, "23503", []any{b.election, a.ana}},
			{"cast for another school's list", a.tenant, `SELECT election_ballot_cast($1, $2, $3)`, "23503", []any{a.election, a.beto, b.list}},
		} {
			if err := exec(tc.asTenant, tc.stmt, tc.args...); code(err) != tc.code {
				t.Fatalf("%s: got %v, want %s", tc.name, err, tc.code)
			}
		}
		// A failed cast left neither a voter nor a Ballot (the voter row of "cast for another school's
		// list" was written and rolled back with the failing Ballot).
		ballotsAndVoters := func(tenant string) (ballots, voters int) {
			t.Helper()
			if err := su.QueryRow(ctx, `SELECT (SELECT count(*) FROM election_ballot_pending WHERE tenant_id = $1)
			                                 + (SELECT count(*) FROM election_ballots WHERE tenant_id = $1),
			                                   (SELECT count(*) FROM election_voters WHERE tenant_id = $1)`, tenant).Scan(&ballots, &voters); err != nil {
				t.Fatal(err)
			}
			return ballots, voters
		}
		if bl, vt := ballotsAndVoters(a.tenant); bl != 0 || vt != 0 {
			t.Fatalf("after failed casts A has %d Ballots and %d voters, want none", bl, vt)
		}
		// Votes in both schools; A's are flushed by A's tenant only. The cast writes the voter and the
		// Ballot together, and a second cast of the same voter hits the registry's primary key.
		if err := exec(a.tenant, `SELECT election_ballot_cast($1, $2, $3)`, a.election, a.ana, a.list); err != nil {
			t.Fatalf("cast A: %v", err)
		}
		if err := exec(a.tenant, `SELECT election_ballot_cast($1, $2, NULL)`, a.election, a.ana); code(err) != "23505" {
			t.Fatalf("second cast of the same voter: got %v, want 23505", err)
		}
		if bl, vt := ballotsAndVoters(a.tenant); bl != 1 || vt != 1 {
			t.Fatalf("A has %d Ballots and %d voters, want 1 and 1", bl, vt)
		}
		if err := exec(b.tenant, `SELECT election_ballot_cast($1, $2, NULL)`, b.election, b.ana); err != nil {
			t.Fatalf("cast B: %v", err)
		}
		var moved int
		if err := asApp(b.tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT election_ballots_flush($1)`, a.election).Scan(&moved)
		}); err != nil || moved != 0 {
			t.Fatalf("flush A's election as B: moved=%d err=%v, want 0", moved, err)
		}
		var pendingA int
		if err := su.QueryRow(ctx, `SELECT count(*) FROM election_ballot_pending WHERE tenant_id = $1`, a.tenant).Scan(&pendingA); err != nil || pendingA != 1 {
			t.Fatalf("A pending = %d (err=%v), want 1", pendingA, err)
		}
		if err := asApp(a.tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT election_ballots_flush($1)`, a.election).Scan(&moved)
		}); err != nil || moved != 1 {
			t.Fatalf("flush A as A: moved=%d err=%v, want 1", moved, err)
		}
		if err := asApp(a.tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT election_ballots_flush($1)`, a.election).Scan(&moved)
		}); err != nil || moved != 0 {
			t.Fatalf("second flush A: moved=%d err=%v, want 0 (idempotent)", moved, err)
		}
		// A flush with pending Ballots rewrites the whole box of the election with new ids, so an early
		// flush leaves no trace in the final box's order.
		boxIDs := func() []string {
			t.Helper()
			rows, err := su.Query(ctx, `SELECT id::text FROM election_ballots WHERE election_id = $1 ORDER BY id`, a.election)
			if err != nil {
				t.Fatal(err)
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatal(err)
			}
			return ids
		}
		first := boxIDs()
		if err := exec(a.tenant, `SELECT election_ballot_cast($1, $2, NULL)`, a.election, a.beto); err != nil {
			t.Fatalf("cast A beto: %v", err)
		}
		if err := asApp(a.tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT election_ballots_flush($1)`, a.election).Scan(&moved)
		}); err != nil || moved != 1 {
			t.Fatalf("flush A after a new cast: moved=%d err=%v, want 1", moved, err)
		}
		if second := boxIDs(); len(second) != 2 || slices.Contains(second, first[0]) {
			t.Fatalf("box ids after the second flush = %v, want 2 new ids (old %v)", second, first)
		}
		// From B, nothing of A is visible.
		for _, q := range []string{
			`SELECT count(*) FROM election_electorates WHERE tenant_id = '` + a.tenant + `'`,
			`SELECT count(*) FROM election_electorate_members WHERE tenant_id = '` + a.tenant + `'`,
			`SELECT count(*) FROM election_voters WHERE tenant_id = '` + a.tenant + `'`,
			`SELECT count(list_id) FROM election_ballots WHERE tenant_id = '` + a.tenant + `'`,
		} {
			var n int
			if err := asApp(b.tenant, func(tx pgx.Tx) error { return tx.QueryRow(ctx, q).Scan(&n) }); err != nil || n != 0 {
				t.Fatalf("%s as B: %d (err=%v), want 0", q, n, err)
			}
		}
		// A sees its own ballot through the granted columns.
		var n int
		if err := asApp(a.tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM election_ballots WHERE election_id = $1`, a.election).Scan(&n)
		}); err != nil || n != 2 {
			t.Fatalf("A box as A: %d (err=%v), want 2", n, err)
		}
		// With no tenant set, policies match nothing and the functions refuse to run.
		if err := pgx.BeginFunc(ctx, env.App, func(tx pgx.Tx) error {
			for _, tbl := range []string{"election_electorates", "election_electorate_members", "election_voters"} {
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Fatalf("%s without tenant: %d rows", tbl, n)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, stmt := range []string{`SELECT election_ballot_cast('` + a.election + `', '` + a.beto + `', NULL)`, `SELECT election_ballots_flush('` + b.election + `')`} {
			err := pgx.BeginFunc(ctx, env.App, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, stmt); return err })
			if code(err) != "42501" {
				t.Fatalf("%s without tenant: got %v, want 42501", stmt, err)
			}
		}
		var pendingB int
		if err := su.QueryRow(ctx, `SELECT count(*) FROM election_ballot_pending WHERE tenant_id = $1`, b.tenant).Scan(&pendingB); err != nil || pendingB != 1 {
			t.Fatalf("B pending = %d (err=%v), want 1 (untouched)", pendingB, err)
		}
	})

	t.Run("S10 ce_app cannot read pending ballots nor the box's order", func(t *testing.T) {
		for _, stmt := range []string{
			`SELECT xmin FROM election_ballots`,
			`SELECT ctid FROM election_ballots`,
			`SELECT xmax FROM election_ballots`,
			`SELECT id FROM election_ballots`,
			`SELECT * FROM election_ballots`,
			`SELECT count(*) FROM election_ballot_pending`,
			`SELECT ctid, xmin FROM election_ballot_pending`,
			`INSERT INTO election_ballots(tenant_id,election_id,list_id) VALUES ('` + a.tenant + `','` + a.election + `',NULL)`,
			`INSERT INTO election_ballot_pending(tenant_id,election_id,list_id) VALUES ('` + a.tenant + `','` + a.election + `',NULL)`,
			`DELETE FROM election_ballots`,
			`SELECT xmin FROM election_voters`,
			`SELECT ctid FROM election_voters`,
			`INSERT INTO election_voters(tenant_id,election_id,roster_member_id) VALUES ('` + a.tenant + `','` + a.election + `','` + a.beto + `')`,
			`UPDATE election_voters SET roster_member_id = roster_member_id`,
			`DELETE FROM election_voters`,
			`DELETE FROM election_electorate_members`,
			`UPDATE election_electorates SET size = 9`,
		} {
			if err := exec(a.tenant, stmt); code(err) != "42501" {
				t.Fatalf("%q as ce_app: got %v, want 42501", stmt, err)
			}
		}
	})

	t.Run("up down up between the previous migration and 00034", func(t *testing.T) {
		conn, err := sql.Open("pgx", env.SuperDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
		if err != nil {
			t.Fatal(err)
		}
		var prev, latest int64
		for _, src := range p.ListSources() {
			if src.Version < 34 && src.Version > prev {
				prev = src.Version
			}
			latest = max(latest, src.Version)
		}
		if prev == 0 {
			t.Fatal("no migration found before 00034")
		}
		if _, err := p.DownTo(ctx, prev); err != nil {
			t.Fatalf("down to %d: %v", prev, err)
		}
		exists := func() []string {
			var out []string
			for _, tbl := range voteTables {
				var ok bool
				if err := su.QueryRow(ctx, `SELECT to_regclass('public.'||$1) IS NOT NULL`, tbl).Scan(&ok); err != nil {
					t.Fatal(err)
				}
				if ok {
					out = append(out, tbl)
				}
			}
			var fns int
			if err := su.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('election_ballot_cast', 'election_ballots_flush')`).Scan(&fns); err != nil {
				t.Fatal(err)
			}
			if fns > 0 {
				out = append(out, "functions")
			}
			return out
		}
		if left := exists(); len(left) != 0 {
			t.Fatalf("after down: %v still exist", left)
		}
		if _, err := p.UpTo(ctx, latest); err != nil {
			t.Fatalf("reapply up to %d: %v", latest, err)
		}
		if got := exists(); len(got) != len(voteTables)+1 {
			t.Fatalf("after up: %v, want all tables and functions", got)
		}
	})
}
