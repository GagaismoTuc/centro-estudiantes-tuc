package elections_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
)

// Draft timestamps are compared at the precision PostgreSQL stores (microseconds): two instants
// inside one microsecond are equal, so window checks see what the database's CHECKs see.

// subMicro is a voting start with a sub-microsecond fraction (…123456789 ns).
var subMicro = example2027.StartsAt.Add(123456789 * time.Nanosecond)

// rejectsCreate asserts CreateElection(d) fails with want and stores no election.
func rejectsCreate(t *testing.T, d elections.Draft, want error) {
	t.Helper()
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateElection(ctx, s.tenant, d); !errors.Is(err, want) {
		t.Fatalf("CreateElection: err = %v, want %v", err, want)
	}
	if n := superCount(t, ctx, "elections", s.tenant); n != 0 {
		t.Fatalf("elections = %d, want 0", n)
	}
}

// S6 (review of PR #91): a submission end 1 ns before the voting start, inside the same
// microsecond, ends at the start once stored, so it is rejected as invalid_submission_period.
func TestSubmicrosecondSubmissionEndAtVotingStartIsRejected(t *testing.T) {
	d := draft2027()
	d.StartsAt, d.EndsAt = subMicro, subMicro.Add(time.Hour)
	d.SubmissionEndsAt = subMicro.Add(-time.Nanosecond)
	rejectsCreate(t, d, elections.ErrInvalidSubmissionPeriod)
}

// S6: a voting end 1 ns after the start, inside the same microsecond, is not after it once stored.
func TestSubmicrosecondVotingEndAtStartIsRejected(t *testing.T) {
	d := draft2027()
	d.StartsAt, d.EndsAt = subMicro, subMicro.Add(time.Nanosecond)
	rejectsCreate(t, d, elections.ErrInvalidWindow)
}

// S6: a submission period shorter than a microsecond is empty once stored.
func TestSubmicrosecondSubmissionPeriodIsRejected(t *testing.T) {
	d := draft2027()
	base := d.SubmissionStartsAt.Add(5 * time.Microsecond)
	d.SubmissionStartsAt, d.SubmissionEndsAt = base.Add(100*time.Nanosecond), base.Add(900*time.Nanosecond)
	rejectsCreate(t, d, elections.ErrInvalidSubmissionPeriod)
}

// S6: "inicio en el pasado" is judged at stored precision: a start in the clock's microsecond is
// not in the past; one microsecond earlier is.
func TestVotingStartVersusNowComparesMicroseconds(t *testing.T) {
	ctx := integration(t)
	now := example2027.StartsAt.Add(-2*time.Hour + 900*time.Nanosecond)
	d := draft2027()
	d.SubmissionStartsAt, d.SubmissionEndsAt = now.Add(-time.Hour), now.Add(-30*time.Minute)
	d.StartsAt = now.Add(-800 * time.Nanosecond) // same microsecond as now
	past := d
	past.StartsAt = d.StartsAt.Add(-time.Microsecond)
	for _, tc := range []struct {
		name string
		d    elections.Draft
		want error
	}{{"same microsecond", d, nil}, {"one microsecond earlier", past, elections.ErrInvalidWindow}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSchool(t, ctx)
			st := newStore(&fakeClock{now})
			if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
				t.Fatal(err)
			}
			_, err := st.CreateElection(ctx, s.tenant, tc.d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateElection: err = %v, want %v", err, tc.want)
			}
			if want := map[bool]int{true: 1, false: 0}[tc.want == nil]; superCount(t, ctx, "elections", s.tenant) != want {
				t.Fatalf("elections = %d, want %d", superCount(t, ctx, "elections", s.tenant), want)
			}
		})
	}
}

// S6: boundaries one microsecond apart are accepted, and every stored timestamp is the input with
// its sub-microsecond digits discarded.
func TestMicrosecondApartBoundariesAreStoredTruncated(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	d := draft2027()
	d.SubmissionStartsAt = d.SubmissionStartsAt.Add(999 * time.Nanosecond)
	d.SubmissionEndsAt = subMicro.Add(-time.Microsecond)
	d.StartsAt = subMicro
	d.EndsAt = subMicro.Add(time.Microsecond)
	e, err := st.CreateElection(ctx, s.tenant, d)
	if err != nil {
		t.Fatalf("CreateElection: %v", err)
	}
	got, err := st.GetElection(ctx, s.tenant, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	tr := func(x time.Time) time.Time { return x.Truncate(time.Microsecond) }
	for _, x := range []elections.Election{e, got} {
		if !x.SubmissionStartsAt.Equal(tr(d.SubmissionStartsAt)) || !x.SubmissionEndsAt.Equal(tr(d.SubmissionEndsAt)) ||
			!x.StartsAt.Equal(tr(d.StartsAt)) || !x.EndsAt.Equal(tr(d.EndsAt)) {
			t.Fatalf("election = %+v, want the draft's timestamps truncated to microseconds", x)
		}
	}
}

// S6/S7: an edit with a sub-microsecond submission end at the voting start is rejected and the
// stored draft is unchanged.
func TestSubmicrosecondBoundaryEditIsRejectedAndChangesNothing(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	e, err := st.CreateElection(ctx, s.tenant, draft2027())
	if err != nil {
		t.Fatal(err)
	}
	d := draft2027()
	d.Name = "Elección 2027 bis"
	d.StartsAt, d.EndsAt = subMicro, subMicro.Add(time.Hour)
	d.SubmissionEndsAt = subMicro.Add(-time.Nanosecond)
	if _, err := st.UpdateDraft(ctx, s.tenant, e.ID, d); !errors.Is(err, elections.ErrInvalidSubmissionPeriod) {
		t.Fatalf("UpdateDraft: err = %v, want %v", err, elections.ErrInvalidSubmissionPeriod)
	}
	got, err := st.GetElection(ctx, s.tenant, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != e.Name || !got.SubmissionEndsAt.Equal(e.SubmissionEndsAt) || !got.StartsAt.Equal(e.StartsAt) ||
		!got.EndsAt.Equal(e.EndsAt) || !got.UpdatedAt.Equal(e.UpdatedAt) || !slices.Equal(got.Positions, e.Positions) {
		t.Fatalf("election = %+v, want unchanged %+v", got, e)
	}
}
