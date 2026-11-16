package elections_test

import (
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
)

// art is Argentina time (UTC-3, no daylight saving).
var art = time.FixedZone("ART", -3*60*60)

func at(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2027, month, day, hour, minute, 0, 0, art)
}

// example2027 is the S6 example: submission 1 to 5 March (inclusive, so it ends at the start of
// 6 March), voting 10 March 08:00 to 18:00, Argentina time.
var example2027 = elections.Election{
	SubmissionStartsAt: at(time.March, 1, 0, 0),
	SubmissionEndsAt:   at(time.March, 6, 0, 0),
	StartsAt:           at(time.March, 10, 8, 0),
	EndsAt:             at(time.March, 10, 18, 0),
}

func TestStateFollowsTheClock(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want elections.Status
	}{
		{"long before submission", at(time.February, 1, 0, 0), elections.StatusDraft},
		{"just before submission", at(time.March, 1, 0, 0).Add(-time.Nanosecond), elections.StatusDraft},
		{"submission starts", at(time.March, 1, 0, 0), elections.StatusSubmission},
		{"during submission", at(time.March, 5, 23, 59), elections.StatusSubmission},
		{"submission ends", at(time.March, 6, 0, 0), elections.StatusScheduled},
		{"7 March (S10 example)", at(time.March, 7, 12, 0), elections.StatusScheduled},
		{"just before start", at(time.March, 10, 8, 0).Add(-time.Nanosecond), elections.StatusScheduled},
		{"starts", at(time.March, 10, 8, 0), elections.StatusOpen},
		{"just before end", at(time.March, 10, 18, 0).Add(-time.Nanosecond), elections.StatusOpen},
		{"10 March 18:00 exactly (S10 example)", at(time.March, 10, 18, 0), elections.StatusClosed},
		{"after end", at(time.April, 1, 0, 0), elections.StatusClosed},
		{"same instant in UTC", time.Date(2027, time.March, 10, 21, 0, 0, 0, time.UTC), elections.StatusClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := elections.State(tc.now, example2027); got != tc.want {
				t.Fatalf("State(%s) = %q, want %q", tc.now, got, tc.want)
			}
		})
	}
}

func TestStatusValuesAreStable(t *testing.T) {
	got := []elections.Status{elections.StatusDraft, elections.StatusSubmission, elections.StatusScheduled, elections.StatusOpen, elections.StatusClosed}
	want := []string{"draft", "submission", "scheduled", "open", "closed"}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("status %d = %q, want %q", i, got[i], want[i])
		}
	}
}
