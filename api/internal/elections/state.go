package elections

import "time"

// Status is an election's phase, derived from the clock and its dates; it is never stored.
type Status string

const (
	StatusDraft      Status = "draft"      // before the submission period
	StatusSubmission Status = "submission" // [SubmissionStartsAt, SubmissionEndsAt)
	StatusScheduled  Status = "scheduled"  // [SubmissionEndsAt, StartsAt)
	StatusOpen       Status = "open"       // [StartsAt, EndsAt)
	StatusClosed     Status = "closed"     // from EndsAt on
)

// State returns e's phase at now. Every window includes its start and excludes its end, so at EndsAt
// exactly the election is closed. Callers pass now from an injected clock (a func() time.Time).
func State(now time.Time, e Election) Status {
	switch {
	case now.Before(e.SubmissionStartsAt):
		return StatusDraft
	case now.Before(e.SubmissionEndsAt):
		return StatusSubmission
	case now.Before(e.StartsAt):
		return StatusScheduled
	case now.Before(e.EndsAt):
		return StatusOpen
	default:
		return StatusClosed
	}
}
