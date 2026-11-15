package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// electionJSON renders e with its state at now; dates go out in UTC.
func electionJSON(e elections.Election, now time.Time) oapi.Election {
	return oapi.Election{Id: e.ID, Name: e.Name, Mode: oapi.ElectionMode(e.Mode),
		SubmissionStartsAt: e.SubmissionStartsAt.UTC(), SubmissionEndsAt: e.SubmissionEndsAt.UTC(),
		StartsAt: e.StartsAt.UTC(), EndsAt: e.EndsAt.UTC(), Positions: e.Positions,
		State: oapi.ElectionState(elections.State(now, e)), Commission: commissionMembersJSON(e.Commission)}
}

// draftBody is oapi.ElectionDraftRequest with every field optional, so a missing or null field gets
// its own code instead of a zero value; mode and positions keep "absent" apart from null.
type draftBody struct {
	Name               *string         `json:"name"`
	Mode               json.RawMessage `json:"mode"`
	SubmissionStartsAt *string         `json:"submissionStartsAt"`
	SubmissionEndsAt   *string         `json:"submissionEndsAt"`
	StartsAt           *string         `json:"startsAt"`
	EndsAt             *string         `json:"endsAt"`
	Positions          json.RawMessage `json:"positions"`
}

// parseTime reads an RFC 3339 date-time; false when missing, null or malformed. time.Parse drops
// fraction digits past the nanosecond, which would turn "10:00:00.0000000001" into a whole minute;
// a non-zero fraction in the text therefore stays non-zero (1 ns), so the store's whole-minute check
// rejects it.
func parseTime(s *string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return t, false
	}
	if t.Nanosecond() == 0 && nonZeroFraction(*s) {
		t = t.Add(time.Nanosecond) // no rollover: the parsed fraction is 0
	}
	return t, true
}

// nonZeroFraction reports whether the seconds fraction of a valid RFC 3339 date-time has a non-zero
// digit. The fraction is the run of digits after the first '.', which only the seconds may carry.
func nonZeroFraction(s string) bool {
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return false
	}
	for _, c := range s[i+1:] {
		if c < '0' || c > '9' {
			return false
		}
		if c != '0' {
			return true
		}
	}
	return false
}

// draft turns the body into an elections.Draft, or the error of the first missing, null or
// malformed field, in the order the store validates: name, window, submission period, positions,
// mode.
func (b draftBody) draft() (elections.Draft, error) {
	var d elections.Draft
	if b.Name == nil {
		return d, elections.ErrInvalidName
	}
	d.Name = *b.Name
	var ok1, ok2 bool
	d.StartsAt, ok1 = parseTime(b.StartsAt)
	d.EndsAt, ok2 = parseTime(b.EndsAt)
	if !ok1 || !ok2 {
		return d, elections.ErrInvalidWindow
	}
	d.SubmissionStartsAt, ok1 = parseTime(b.SubmissionStartsAt)
	d.SubmissionEndsAt, ok2 = parseTime(b.SubmissionEndsAt)
	if !ok1 || !ok2 {
		return d, elections.ErrInvalidSubmissionPeriod
	}
	d.Positions = slices.Clone(elections.DefaultPositions)
	if b.Positions != nil {
		d.Positions = nil // null stays nil: invalid_positions
		if json.Unmarshal(b.Positions, &d.Positions) != nil {
			return d, elections.ErrInvalidPositions
		}
		if d.Positions == nil {
			return d, elections.ErrInvalidPositions
		}
	}
	d.Mode = elections.ModeSecret
	if b.Mode != nil {
		var mode *string
		if json.Unmarshal(b.Mode, &mode) != nil || mode == nil {
			return d, elections.ErrModeNotSupported
		}
		d.Mode = *mode
	}
	return d, nil
}

func writeDraftError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, elections.ErrInvalidName):
		writeError(w, http.StatusUnprocessableEntity, "invalid_name")
	case errors.Is(err, elections.ErrInvalidWindow):
		writeError(w, http.StatusUnprocessableEntity, "invalid_window")
	case errors.Is(err, elections.ErrInvalidSubmissionPeriod):
		writeError(w, http.StatusUnprocessableEntity, "invalid_submission_period")
	case errors.Is(err, elections.ErrInvalidPositions):
		writeError(w, http.StatusUnprocessableEntity, "invalid_positions")
	case errors.Is(err, elections.ErrModeNotSupported):
		writeError(w, http.StatusUnprocessableEntity, "mode_not_supported")
	case errors.Is(err, elections.ErrElectionInProgress):
		writeError(w, http.StatusConflict, "election_in_progress")
	case errors.Is(err, elections.ErrNotDraft):
		writeError(w, http.StatusConflict, "election_not_draft")
	default:
		writeElectionsError(w, op, err)
	}
}

// readDraft authorizes sess to write the draft before reading the body, then decodes it. It writes
// every error response.
func (s *server) readDraft(w http.ResponseWriter, r *http.Request, op string) (auth.Session, elections.Draft, bool) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return sess, elections.Draft{}, false
	}
	if err := s.elections.AuthorizeDraft(r.Context(), sess); err != nil {
		writeElectionsError(w, op, err)
		return sess, elections.Draft{}, false
	}
	var body draftBody
	if !decodeStrict(w, r, &body) {
		return sess, elections.Draft{}, false
	}
	d, err := body.draft()
	if err != nil {
		writeDraftError(w, op, err)
		return sess, elections.Draft{}, false
	}
	return sess, d, true
}

func (s *server) PostElection(w http.ResponseWriter, r *http.Request) {
	sess, d, ok := s.readDraft(w, r, "create election")
	if !ok {
		return
	}
	e, err := s.elections.Create(r.Context(), sess, d)
	if err != nil {
		writeDraftError(w, "create election", err)
		return
	}
	writeJSON(w, http.StatusCreated, electionJSON(e, s.now()))
}

func (s *server) PutElection(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, d, ok := s.readDraft(w, r, "edit election")
	if !ok {
		return
	}
	e, err := s.elections.Edit(r.Context(), sess, electionID, d)
	if err != nil {
		writeDraftError(w, "edit election", err)
		return
	}
	writeJSON(w, http.StatusOK, electionJSON(e, s.now()))
}

func (s *server) GetElection(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	e, err := s.elections.View(r.Context(), sess, electionID)
	if err != nil {
		writeElectionsError(w, "read election", err)
		return
	}
	writeJSON(w, http.StatusOK, electionJSON(e, s.now()))
}

func (s *server) GetCurrentElection(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	e, err := s.elections.ViewCurrent(r.Context(), sess)
	if err != nil {
		writeElectionsError(w, "read current election", err)
		return
	}
	writeJSON(w, http.StatusOK, electionJSON(e, s.now()))
}
