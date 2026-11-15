package elections_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

var (
	tdb      *testdb.Env
	setupErr error
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	var stop func()
	tdb, stop, setupErr = testdb.Start(context.Background())
	code := m.Run()
	stop()
	os.Exit(code)
}

func integration(t *testing.T) context.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker; skipped with -short")
	}
	if setupErr != nil {
		t.Fatalf("setup: %v", setupErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

// school is a fresh tenant: ana and beto have accounts, carla is on the roster without one.
type school struct {
	tenant, ana, beto, carla string
	anaUser, carlaName       string
}

var schools int

func newSchool(t *testing.T, ctx context.Context) school {
	t.Helper()
	schools++
	var s school
	su := tdb.Super
	if err := su.QueryRow(ctx, `INSERT INTO organizations (kind, name) VALUES ('school', $1) RETURNING id::text`,
		fmt.Sprintf("Escuela %d", schools)).Scan(&s.tenant); err != nil {
		t.Fatal(err)
	}
	s.carlaName = "Carla Cruz"
	for i, m := range []struct {
		id      *string
		name    string
		account bool
	}{{&s.ana, "Ana Acosta", true}, {&s.beto, "Beto Bravo", true}, {&s.carla, s.carlaName, false}} {
		if err := su.QueryRow(ctx, `INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, $2, $3) RETURNING id::text`,
			s.tenant, m.name, fmt.Sprint(100+i)).Scan(m.id); err != nil {
			t.Fatal(err)
		}
		if !m.account {
			continue
		}
		var user string
		if err := su.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
			fmt.Sprintf("u%d-%d@example.com", schools, i)).Scan(&user); err != nil {
			t.Fatal(err)
		}
		if _, err := su.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role, roster_member_id) VALUES ($1, $2, 'student', $3)`,
			s.tenant, user, *m.id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			s.anaUser = user
		}
	}
	return s
}

func newStore(clk *fakeClock) *elections.Store {
	return elections.NewStore(tdb.App, clk.Now)
}

// today is before the example election's submission period, so a new election is a draft.
func today() *fakeClock { return &fakeClock{time.Date(2026, time.November, 15, 12, 0, 0, 0, art)} }

func draft2027() elections.Draft {
	return elections.Draft{
		Name:               "Elección 2027",
		Mode:               elections.ModeSecret,
		SubmissionStartsAt: example2027.SubmissionStartsAt,
		SubmissionEndsAt:   example2027.SubmissionEndsAt,
		StartsAt:           example2027.StartsAt,
		EndsAt:             example2027.EndsAt,
		Positions:          slices.Clone(elections.DefaultPositions),
	}
}

func memberIDs(c elections.Commission) []string {
	out := []string{}
	for _, m := range c.Members {
		out = append(out, m.RosterMemberID)
	}
	slices.Sort(out)
	return out
}

func sorted(ids ...string) []string { slices.Sort(ids); return ids }

// superCount counts rows of table for tenant as superuser, bypassing RLS.
func superCount(t *testing.T, ctx context.Context, table, tenant string) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNewStoreRejectsANilClock(t *testing.T) {
	defer func() {
		if got := recover(); got != "elections: NewStore needs a clock" {
			t.Fatalf("panic = %v, want %q", got, "elections: NewStore needs a clock")
		}
	}()
	elections.NewStore(nil, nil)
}

func TestDefaultPositions(t *testing.T) {
	want := []string{"Presidente", "Vicepresidente", "Secretario", "Tesorero"}
	if !slices.Equal(elections.DefaultPositions, want) {
		t.Fatalf("DefaultPositions = %v, want %v", elections.DefaultPositions, want)
	}
}

// S2: members are roster members with an account; a repeated id is stored once.
func TestReplaceCommissionStoresEachMemberOnceWithName(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	got, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana, s.beto, s.ana})
	if err != nil {
		t.Fatalf("ReplaceCommission: %v", err)
	}
	if ids := memberIDs(got); !slices.Equal(ids, sorted(s.ana, s.beto)) {
		t.Fatalf("members = %v, want ana and beto once", ids)
	}
	cur, err := st.CurrentCommission(ctx, s.tenant)
	if err != nil {
		t.Fatalf("CurrentCommission: %v", err)
	}
	if cur.ID != got.ID || !slices.Equal(memberIDs(cur), sorted(s.ana, s.beto)) {
		t.Fatalf("current = %+v, want %+v", cur, got)
	}
	names := []string{cur.Members[0].FullName, cur.Members[1].FullName}
	if !slices.Equal(names, []string{"Ana Acosta", "Beto Bravo"}) {
		t.Fatalf("names = %v, want ordered by name", names)
	}
	if n := superCount(t, ctx, "electoral_commission_members", s.tenant); n != 2 {
		t.Fatalf("stored members = %d, want 2", n)
	}
}

// S2: an id is a UUID, so the same student sent in upper and lower case is one member, as
// PostgreSQL reads it, and is stored once.
func TestReplaceCommissionCountsAnIdOnceRegardlessOfCase(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	got, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana, strings.ToUpper(s.ana)})
	if err != nil {
		t.Fatalf("ReplaceCommission: %v", err)
	}
	if ids := memberIDs(got); !slices.Equal(ids, []string{s.ana}) {
		t.Fatalf("members = %v, want ana once", ids)
	}
	cur, err := st.CurrentCommission(ctx, s.tenant)
	if err != nil {
		t.Fatalf("CurrentCommission: %v", err)
	}
	if ids := memberIDs(cur); !slices.Equal(ids, []string{s.ana}) {
		t.Fatalf("current members = %v, want ana once", ids)
	}
	if n := superCount(t, ctx, "electoral_commission_members", s.tenant); n != 1 {
		t.Fatalf("stored members = %d, want 1", n)
	}
}

// S2: rejected lists write nothing.
func TestReplaceCommissionRejectsInvalidMembers(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	other := newSchool(t, ctx)
	st := newStore(today())
	for _, tc := range []struct {
		name string
		ids  []string
		want error
	}{
		{"empty list", nil, elections.ErrEmptyCommission},
		{"roster member without account", []string{s.ana, s.carla}, elections.ErrNoAccount},
		{"roster member of another school", []string{s.ana, other.ana}, elections.ErrNotFound},
		{"not a uuid", []string{"nope"}, elections.ErrNotFound},
		{"unknown uuid", []string{"00000000-0000-0000-0000-000000000000"}, elections.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.ReplaceCommission(ctx, s.tenant, tc.ids); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			for _, table := range []string{"electoral_commissions", "electoral_commission_members"} {
				if n := superCount(t, ctx, table, s.tenant); n != 0 {
					t.Fatalf("%s rows = %d, want 0", table, n)
				}
			}
		})
	}
	if _, err := st.CurrentCommission(ctx, s.tenant); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("CurrentCommission err = %v, want ErrNotFound", err)
	}
}

// S3: replacing keeps one current commission and swaps its members.
func TestReplaceCommissionSwapsMembersOfTheCurrentCommission(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	first, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.ReplaceCommission(ctx, s.tenant, []string{s.beto})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("commission id changed from %s to %s, want the same current commission", first.ID, second.ID)
	}
	if ids := memberIDs(second); !slices.Equal(ids, []string{s.beto}) {
		t.Fatalf("members = %v, want only beto", ids)
	}
	if n := superCount(t, ctx, "electoral_commissions", s.tenant); n != 1 {
		t.Fatalf("commissions = %d, want 1", n)
	}
}

// S3: once its election opened, the commission is frozen; nothing changes.
func TestReplaceCommissionIsFrozenOnceTheElectionOpens(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	clk := today()
	st := newStore(clk)
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateElection(ctx, s.tenant, draft2027()); err != nil {
		t.Fatal(err)
	}
	clk.t = example2027.StartsAt.Add(-time.Second) // scheduled: still replaceable
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.beto}); err != nil {
		t.Fatalf("replace while scheduled: %v", err)
	}
	clk.t = example2027.StartsAt
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); !errors.Is(err, elections.ErrElectionFrozen) {
		t.Fatalf("err = %v, want ErrElectionFrozen", err)
	}
	cur, err := st.CurrentCommission(ctx, s.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if ids := memberIDs(cur); !slices.Equal(ids, []string{s.beto}) {
		t.Fatalf("members = %v, want beto unchanged", ids)
	}
}

func TestIsCommissionMember(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	other := newSchool(t, ctx)
	st := newStore(today())
	if ok, err := st.IsCommissionMember(ctx, s.tenant, s.anaUser); err != nil || ok {
		t.Fatalf("before designation: %v, %v; want false", ok, err)
	}
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.IsCommissionMember(ctx, s.tenant, s.anaUser); err != nil || !ok {
		t.Fatalf("member: %v, %v; want true", ok, err)
	}
	if ok, err := st.IsCommissionMember(ctx, other.tenant, s.anaUser); err != nil || ok {
		t.Fatalf("other school: %v, %v; want false", ok, err)
	}
}

// S6, S7, S9: the draft is stored as given, with its positions in order.
func TestCreateElectionStoresTheDraft(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	com, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana})
	if err != nil {
		t.Fatal(err)
	}
	d := draft2027()
	e, err := st.CreateElection(ctx, s.tenant, d)
	if err != nil {
		t.Fatalf("CreateElection: %v", err)
	}
	got, err := st.GetElection(ctx, s.tenant, e.ID)
	if err != nil {
		t.Fatalf("GetElection: %v", err)
	}
	cur, err := st.CurrentElection(ctx, s.tenant)
	if err != nil {
		t.Fatalf("CurrentElection: %v", err)
	}
	for _, x := range []elections.Election{e, got, cur} {
		if x.ID != e.ID || x.CommissionID != com.ID || x.Name != d.Name || x.Mode != elections.ModeSecret ||
			!x.SubmissionStartsAt.Equal(d.SubmissionStartsAt) || !x.SubmissionEndsAt.Equal(d.SubmissionEndsAt) ||
			!x.StartsAt.Equal(d.StartsAt) || !x.EndsAt.Equal(d.EndsAt) || x.PublishedAt != nil || x.AnnulledAt != nil ||
			!slices.Equal(x.Positions, elections.DefaultPositions) {
			t.Fatalf("election = %+v, want the draft %+v", x, d)
		}
	}
	if elections.State(today().Now(), got) != elections.StatusDraft {
		t.Fatalf("state = %q, want draft", elections.State(today().Now(), got))
	}
}

func TestCreateElectionNeedsACommission(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	if _, err := newStore(today()).CreateElection(ctx, s.tenant, draft2027()); !errors.Is(err, elections.ErrNoCommission) {
		t.Fatalf("err = %v, want ErrNoCommission", err)
	}
	if n := superCount(t, ctx, "elections", s.tenant); n != 0 {
		t.Fatalf("elections = %d, want 0", n)
	}
}

// S7: position names are unique without regard to case.
func TestCreateElectionRejectsRepeatedPositions(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	d := draft2027()
	d.Positions = []string{"Presidente", "presidente"}
	if _, err := st.CreateElection(ctx, s.tenant, d); !errors.Is(err, elections.ErrInvalidPositions) {
		t.Fatalf("err = %v, want ErrInvalidPositions", err)
	}
	for _, table := range []string{"elections", "election_positions"} {
		if n := superCount(t, ctx, table, s.tenant); n != 0 {
			t.Fatalf("%s rows = %d, want 0", table, n)
		}
	}
}

// S8: while an election is in progress another one is refused.
func TestCreateElectionRefusesASecondInProgress(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateElection(ctx, s.tenant, draft2027()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateElection(ctx, s.tenant, draft2027()); !errors.Is(err, elections.ErrElectionInProgress) {
		t.Fatalf("err = %v, want ErrElectionInProgress", err)
	}
	if n := superCount(t, ctx, "elections", s.tenant); n != 1 {
		t.Fatalf("elections = %d, want 1", n)
	}
}

// S8: two simultaneous creates, exactly one wins; the database decides.
func TestConcurrentCreateElectionExactlyOneWins(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	const n = 2
	errs := make([]error, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range n {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			_, errs[i] = st.CreateElection(ctx, s.tenant, draft2027())
		}()
	}
	start.Done()
	done.Wait()
	var ok, inProgress int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, elections.ErrElectionInProgress):
			inProgress++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || inProgress != 1 {
		t.Fatalf("ok=%d in progress=%d, want 1 and 1", ok, inProgress)
	}
	if got := superCount(t, ctx, "elections", s.tenant); got != 1 {
		t.Fatalf("elections = %d, want 1", got)
	}
	if got := superCount(t, ctx, "election_positions", s.tenant); got != len(elections.DefaultPositions) {
		t.Fatalf("positions = %d, want %d", got, len(elections.DefaultPositions))
	}
}

// S6, S7: editing the draft replaces its fields and positions.
func TestUpdateDraftReplacesFieldsAndPositions(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	clk := today()
	st := newStore(clk)
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	e, err := st.CreateElection(ctx, s.tenant, draft2027())
	if err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(time.Hour)
	d := draft2027()
	d.Name = "Elección 2027 bis"
	d.EndsAt = d.EndsAt.Add(time.Hour)
	d.Positions = []string{"Tesorero", "Presidenta", "Vocal"}
	got, err := st.UpdateDraft(ctx, s.tenant, e.ID, d)
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	read, err := st.GetElection(ctx, s.tenant, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []elections.Election{got, read} {
		if x.Name != d.Name || !x.EndsAt.Equal(d.EndsAt) || !slices.Equal(x.Positions, d.Positions) || !x.UpdatedAt.Equal(clk.t) {
			t.Fatalf("election = %+v, want name %q, end %s, positions %v, updated %s", x, d.Name, d.EndsAt, d.Positions, clk.t)
		}
	}
	if n := superCount(t, ctx, "election_positions", s.tenant); n != 3 {
		t.Fatalf("positions = %d, want 3", n)
	}
}

// S7: only a draft can be edited; a rejected edit changes nothing.
func TestUpdateDraftRefusesOnceSubmissionStarts(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	clk := today()
	st := newStore(clk)
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	e, err := st.CreateElection(ctx, s.tenant, draft2027())
	if err != nil {
		t.Fatal(err)
	}
	clk.t = example2027.SubmissionStartsAt
	d := draft2027()
	d.Name = "Tarde"
	if _, err := st.UpdateDraft(ctx, s.tenant, e.ID, d); !errors.Is(err, elections.ErrNotDraft) {
		t.Fatalf("err = %v, want ErrNotDraft", err)
	}
	read, err := st.GetElection(ctx, s.tenant, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Name != "Elección 2027" {
		t.Fatalf("name = %q, want unchanged", read.Name)
	}
}

// S12: one school never reads or writes another school's commission or election through the store.
func TestStoreIsolatesSchools(t *testing.T) {
	ctx := integration(t)
	a := newSchool(t, ctx)
	b := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, a.tenant, []string{a.ana}); err != nil {
		t.Fatal(err)
	}
	e, err := st.CreateElection(ctx, a.tenant, draft2027())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CurrentCommission(ctx, b.tenant); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("CurrentCommission(B) err = %v, want ErrNotFound", err)
	}
	if _, err := st.CurrentElection(ctx, b.tenant); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("CurrentElection(B) err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetElection(ctx, b.tenant, e.ID); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("GetElection(B, A's id) err = %v, want ErrNotFound", err)
	}
	d := draft2027()
	d.Name = "Hackeada"
	if _, err := st.UpdateDraft(ctx, b.tenant, e.ID, d); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("UpdateDraft(B, A's id) err = %v, want ErrNotFound", err)
	}
	if _, err := st.ReplaceCommission(ctx, b.tenant, []string{a.beto}); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("ReplaceCommission(B, A's member) err = %v, want ErrNotFound", err)
	}
	read, err := st.GetElection(ctx, a.tenant, e.ID)
	if err != nil || read.Name != "Elección 2027" {
		t.Fatalf("A's election = %+v, %v; want unchanged", read, err)
	}
	for _, table := range []string{"electoral_commissions", "electoral_commission_members", "elections", "election_positions"} {
		if n := superCount(t, ctx, table, b.tenant); n != 0 {
			t.Fatalf("%s rows of B = %d, want 0", table, n)
		}
	}
	if _, err := st.GetElection(ctx, a.tenant, "nope"); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("GetElection(not a uuid) err = %v, want ErrNotFound", err)
	}
}
