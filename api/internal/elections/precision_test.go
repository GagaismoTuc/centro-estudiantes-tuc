package elections_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
)

// Election times are whole minutes (owner decision on PR #91), so no window check ever compares
// values finer than PostgreSQL stores. The sub-microsecond edges of earlier review rounds are now
// rejected by the whole-minute rule; voting-window fields are checked before submission fields.

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

// S6 (review of PR #91): a submission end 1 ns before a voting start with a sub-second fraction is
// rejected; the voting start is not a whole minute, and the window is checked first.
func TestSubmicrosecondSubmissionEndAtVotingStartIsRejected(t *testing.T) {
	d := draft2027()
	d.StartsAt, d.EndsAt = subMicro, subMicro.Add(time.Hour)
	d.SubmissionEndsAt = subMicro.Add(-time.Nanosecond)
	rejectsCreate(t, d, elections.ErrInvalidWindow)
}

// S6: a voting end 1 ns after the start is not a whole minute.
func TestSubmicrosecondVotingEndAtStartIsRejected(t *testing.T) {
	d := draft2027()
	d.StartsAt, d.EndsAt = subMicro, subMicro.Add(time.Nanosecond)
	rejectsCreate(t, d, elections.ErrInvalidWindow)
}

// S6: a submission period shorter than a microsecond is not made of whole minutes.
func TestSubmicrosecondSubmissionPeriodIsRejected(t *testing.T) {
	d := draft2027()
	base := d.SubmissionStartsAt.Add(5 * time.Microsecond)
	d.SubmissionStartsAt, d.SubmissionEndsAt = base.Add(100*time.Nanosecond), base.Add(900*time.Nanosecond)
	rejectsCreate(t, d, elections.ErrInvalidSubmissionPeriod)
}

// S6: "inicio en el pasado" compares the whole-minute start with the real clock: a start 900 ns
// behind the clock is in the past; a start 1 ns ahead of it is not.
func TestVotingStartVersusNowUsesTheRealClock(t *testing.T) {
	ctx := integration(t)
	d := draft2027()
	for _, tc := range []struct {
		name string
		now  time.Time
		want error
	}{
		{"clock 1 ns before the start", d.StartsAt.Add(-time.Nanosecond), nil},
		{"clock 900 ns after the start", d.StartsAt.Add(900 * time.Nanosecond), elections.ErrInvalidWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSchool(t, ctx)
			st := newStore(&fakeClock{tc.now})
			if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
				t.Fatal(err)
			}
			_, err := st.CreateElection(ctx, s.tenant, d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateElection: err = %v, want %v", err, tc.want)
			}
			if want := map[bool]int{true: 1, false: 0}[tc.want == nil]; superCount(t, ctx, "elections", s.tenant) != want {
				t.Fatalf("elections = %d, want %d", superCount(t, ctx, "elections", s.tenant), want)
			}
		})
	}
}

// S6: boundaries one minute apart are accepted and stored exactly as given.
func TestMinuteApartBoundariesAreStoredExactly(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	st := newStore(today())
	if _, err := st.ReplaceCommission(ctx, s.tenant, []string{s.ana}); err != nil {
		t.Fatal(err)
	}
	d := draft2027()
	d.SubmissionEndsAt = d.StartsAt.Add(-time.Minute)
	d.EndsAt = d.StartsAt.Add(time.Minute)
	e, err := st.CreateElection(ctx, s.tenant, d)
	if err != nil {
		t.Fatalf("CreateElection: %v", err)
	}
	got, err := st.GetElection(ctx, s.tenant, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []elections.Election{e, got} {
		if !x.SubmissionStartsAt.Equal(d.SubmissionStartsAt) || !x.SubmissionEndsAt.Equal(d.SubmissionEndsAt) ||
			!x.StartsAt.Equal(d.StartsAt) || !x.EndsAt.Equal(d.EndsAt) {
			t.Fatalf("election = %+v, want the draft's timestamps", x)
		}
	}
}

// S6/S7: an edit with a sub-second voting start is rejected and the stored draft is unchanged.
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
	if _, err := st.UpdateDraft(ctx, s.tenant, e.ID, d); !errors.Is(err, elections.ErrInvalidWindow) {
		t.Fatalf("UpdateDraft: err = %v, want %v", err, elections.ErrInvalidWindow)
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
