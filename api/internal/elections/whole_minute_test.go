package elections_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

// Owner decision on PR #91: election times are whole minutes. A draft whose submission start or
// end, voting start or end carries seconds or fractions of a second is rejected with that field's
// error, and nothing is stored.

// subMinuteCases perturbs one field of draft2027 by 30 s (08:00:30) or 0.5 s (08:00:00.5); every
// other field stays a valid whole minute in the future.
func subMinuteCases() []struct {
	name string
	d    elections.Draft
	want error
} {
	type field struct {
		name string
		set  func(*elections.Draft, time.Duration)
		want error
	}
	fields := []field{
		{"submissionStartsAt", func(d *elections.Draft, x time.Duration) { d.SubmissionStartsAt = d.SubmissionStartsAt.Add(x) }, elections.ErrInvalidSubmissionPeriod},
		{"submissionEndsAt", func(d *elections.Draft, x time.Duration) { d.SubmissionEndsAt = d.SubmissionEndsAt.Add(x) }, elections.ErrInvalidSubmissionPeriod},
		{"startsAt", func(d *elections.Draft, x time.Duration) { d.StartsAt = d.StartsAt.Add(x) }, elections.ErrInvalidWindow},
		{"endsAt", func(d *elections.Draft, x time.Duration) { d.EndsAt = d.EndsAt.Add(x) }, elections.ErrInvalidWindow},
	}
	var out []struct {
		name string
		d    elections.Draft
		want error
	}
	for _, f := range fields {
		for _, p := range []struct {
			name string
			x    time.Duration
		}{{"seconds", 30 * time.Second}, {"fraction of a second", 500 * time.Millisecond}} {
			d := draft2027()
			f.set(&d, p.x)
			out = append(out, struct {
				name string
				d    elections.Draft
				want error
			}{f.name + " with " + p.name, d, f.want})
		}
	}
	return out
}

// S6: creating a draft with a sub-minute timestamp is rejected with the field's error; no row.
func TestCreateElectionRejectsSubMinuteTimestamps(t *testing.T) {
	ctx := integration(t)
	for _, tc := range subMinuteCases() {
		t.Run(tc.name, func(t *testing.T) {
			s := newSchool(t, ctx)
			st := newStore(today())
			if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateElection(ctx, s.tenant, tc.d); !errors.Is(err, tc.want) {
				t.Fatalf("CreateElection: err = %v, want %v", err, tc.want)
			}
			for _, table := range []string{"elections", "election_positions"} {
				if n := superCount(t, ctx, table, s.tenant); n != 0 {
					t.Fatalf("%s = %d, want 0", table, n)
				}
			}
		})
	}
}

// S6: editing a draft with a sub-minute timestamp is rejected with the field's error and the stored
// draft is unchanged.
func TestUpdateDraftRejectsSubMinuteTimestamps(t *testing.T) {
	ctx := integration(t)
	for _, tc := range subMinuteCases() {
		t.Run(tc.name, func(t *testing.T) {
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
			d := tc.d
			d.Name = "Elección 2027 bis"
			d.Positions = []string{"Vocal"}
			if _, err := st.UpdateDraft(ctx, s.tenant, e.ID, d); !errors.Is(err, tc.want) {
				t.Fatalf("UpdateDraft: err = %v, want %v", err, tc.want)
			}
			got, err := st.GetElection(ctx, s.tenant, e.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != e.Name || !got.SubmissionStartsAt.Equal(e.SubmissionStartsAt) || !got.SubmissionEndsAt.Equal(e.SubmissionEndsAt) ||
				!got.StartsAt.Equal(e.StartsAt) || !got.EndsAt.Equal(e.EndsAt) || !got.UpdatedAt.Equal(e.UpdatedAt) ||
				!slices.Equal(got.Positions, e.Positions) {
				t.Fatalf("election = %+v, want unchanged %+v", got, e)
			}
		})
	}
}

// S6 (codex, review round 3 of PR #91): a start 1 ns before the clock is in the past.
func TestReviewPastStartWithinMicrosecond(t *testing.T) {
	ctx := context.Background()
	db, stop, err := testdb.Start(ctx)
	defer stop()
	if err != nil {
		t.Fatal(err)
	}
	var tenant string
	if err = db.Super.QueryRow(ctx, `INSERT INTO organizations(kind,name) VALUES ('school','Review') RETURNING id::text`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Super.Exec(ctx, `INSERT INTO electoral_commissions(tenant_id) VALUES($1)`, tenant); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2027, 3, 10, 12, 0, 0, 900, time.UTC)
	start := now.Add(-time.Nanosecond)
	s := elections.NewStore(db.App, func() time.Time { return now })
	_, err = s.CreateElection(ctx, tenant, elections.Draft{Name: "Election", Mode: "secret", SubmissionStartsAt: now.Add(-2 * time.Hour), SubmissionEndsAt: now.Add(-time.Hour), StartsAt: start, EndsAt: now.Add(time.Hour), Positions: []string{"President"}})
	if !errors.Is(err, elections.ErrInvalidWindow) {
		t.Errorf("past start error = %v, want invalid window", err)
	}
	var count int
	if err = db.Super.QueryRow(ctx, `SELECT count(*) FROM elections WHERE tenant_id=$1`, tenant).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("stored elections = %d, want 0", count)
	}
}
