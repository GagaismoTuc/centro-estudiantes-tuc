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
	// ErrInvalidWindow means the voting window ends at or before its start, or starts in the past.
	ErrInvalidWindow = errors.New("elections: invalid voting window")
	// ErrInvalidSubmissionPeriod means the submission period is empty or does not end by the start.
	ErrInvalidSubmissionPeriod = errors.New("elections: invalid submission period")
	// ErrModeNotSupported means a mode other than ModeSecret.
	ErrModeNotSupported = errors.New("elections: mode not supported")
)

// checkDraft validates d at now, in this order: name, voting window, submission period, positions,
// mode. It returns d with the name and the position names trimmed.
func checkDraft(now time.Time, d Draft) (Draft, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || utf8.RuneCountInString(d.Name) > MaxNameLen {
		return d, ErrInvalidName
	}
	if !d.EndsAt.After(d.StartsAt) || d.StartsAt.Before(now) {
		return d, ErrInvalidWindow
	}
	if !d.SubmissionEndsAt.After(d.SubmissionStartsAt) || d.SubmissionEndsAt.After(d.StartsAt) {
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
