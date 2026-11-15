package elections

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxNameLen is the longest election or position name, in characters.
const MaxNameLen = 120

var (
	// ErrInvalidName means the name is blank or longer than MaxNameLen characters.
	ErrInvalidName = errors.New("elections: invalid name")
	// ErrInvalidWindow means the voting start or end is not a whole minute, the window ends at or
	// before its start, or it starts in the past.
	ErrInvalidWindow = errors.New("elections: invalid voting window")
	// ErrInvalidSubmissionPeriod means the submission start or end is not a whole minute, the period
	// is empty, or it does not end before the voting start: ending exactly at StartsAt is rejected too.
	ErrInvalidSubmissionPeriod = errors.New("elections: invalid submission period")
	// ErrModeNotSupported means a mode other than ModeSecret.
	ErrModeNotSupported = errors.New("elections: mode not supported")
)

// checkDraft validates d at now, in this order: voting window and then submission period are whole
// minutes, name, voting window, submission period, positions, mode. Election times are whole
// minutes, so the comparisons see exactly the values PostgreSQL stores; "starts in the past" compares
// StartsAt with now as given. It returns d with the name and the position names trimmed.
func checkDraft(now time.Time, d Draft) (Draft, error) {
	if !wholeMinute(d.StartsAt) || !wholeMinute(d.EndsAt) {
		return d, ErrInvalidWindow
	}
	if !wholeMinute(d.SubmissionStartsAt) || !wholeMinute(d.SubmissionEndsAt) {
		return d, ErrInvalidSubmissionPeriod
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || utf8.RuneCountInString(d.Name) > MaxNameLen {
		return d, ErrInvalidName
	}
	if !d.EndsAt.After(d.StartsAt) || d.StartsAt.Before(now) {
		return d, ErrInvalidWindow
	}
	if !d.SubmissionEndsAt.After(d.SubmissionStartsAt) || !d.SubmissionEndsAt.Before(d.StartsAt) {
		return d, ErrInvalidSubmissionPeriod
	}
	if len(d.Positions) == 0 {
		return d, ErrInvalidPositions
	}
	positions := make([]string, len(d.Positions))
	seen := map[string]bool{}
	for i, p := range d.Positions {
		p = strings.TrimSpace(p)
		key := strings.ToLower(p)
		if p == "" || utf8.RuneCountInString(p) > MaxNameLen || seen[key] {
			return d, ErrInvalidPositions
		}
		seen[key], positions[i] = true, p
	}
	d.Positions = positions
	if d.Mode != ModeSecret {
		return d, ErrModeNotSupported
	}
	return d, nil
}

// wholeMinute reports whether t has no seconds and no fraction of a second, in UTC.
func wholeMinute(t time.Time) bool {
	t = t.UTC()
	return t.Second() == 0 && t.Nanosecond() == 0
}
