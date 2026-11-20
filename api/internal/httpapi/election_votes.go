package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// writeVoteError maps the errors of a vote, the turnout and the result. An unexpected error is logged with
// its SQLSTATE only: never the error text, the choice or anything about the voter (S11, CS-8).
func writeVoteError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, http.StatusForbidden, "mfa_required")
	case errors.Is(err, elections.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, elections.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, elections.ErrElectionAnnulled):
		writeError(w, http.StatusConflict, "election_annulled")
	case errors.Is(err, elections.ErrElectionNotOpen):
		writeError(w, http.StatusConflict, "election_not_open")
	case errors.Is(err, elections.ErrNoApprovedLists):
		writeError(w, http.StatusConflict, "no_approved_lists")
	case errors.Is(err, elections.ErrNotInElectorate):
		writeError(w, http.StatusForbidden, "not_in_electorate")
	case errors.Is(err, elections.ErrInvalidChoice):
		writeError(w, http.StatusUnprocessableEntity, "invalid_choice")
	case errors.Is(err, elections.ErrAlreadyVoted):
		writeError(w, http.StatusConflict, "already_voted")
	case errors.Is(err, elections.ErrResultsNotAvailable):
		writeError(w, http.StatusConflict, "results_not_available")
	default:
		var pgErr *pgconn.PgError
		code := "none"
		if errors.As(err, &pgErr) {
			code = pgErr.Code
		}
		slog.Error(op+" failed", "sqlstate", code)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

// voteBody is oapi.VoteRequest with the choice optional, so a missing or null choice gets
// invalid_choice from the store instead of a bad request.
type voteBody struct {
	Choice *string `json:"choice"`
}

func (s *server) PostElectionVote(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	var body voteBody
	if !decodeStrict(w, r, &body) {
		return
	}
	var choice string
	if body.Choice != nil {
		choice = *body.Choice
	}
	if err := s.elections.Vote(r.Context(), sess, electionID, choice); err != nil {
		writeVoteError(w, "cast vote", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) GetElectionTurnout(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	t, err := s.elections.ViewTurnout(r.Context(), sess, electionID)
	if err != nil {
		writeVoteError(w, "read turnout", err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.Turnout{Voted: t.Voted, Electorate: t.Electorate, HasVoted: t.HasVoted})
}

// GetElectionResults shows the result from the election's end on (see elections.Store.ViewResult):
// per list counts only once published, never anything about a voter or a Ballot.
func (s *server) GetElectionResults(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	res, err := s.elections.ViewResult(r.Context(), sess, electionID)
	if err != nil {
		writeVoteError(w, "read results", err)
		return
	}
	writeJSON(w, http.StatusOK, electionResult(res))
}

func electionResult(res elections.Result) oapi.ElectionResult {
	if res.Annulled {
		return oapi.ElectionResult{Status: oapi.ElectionResultStatusAnnulled, AnnulmentReason: &res.AnnulmentReason}
	}
	mode, outcome := oapi.Secret, oapi.ElectionResultOutcome(res.Outcome)
	lists := make([]oapi.ElectionResultList, 0, len(res.Lists))
	for _, l := range res.Lists {
		lists = append(lists, oapi.ElectionResultList{Id: l.ID, Name: l.Name, Votes: l.Votes})
	}
	out := oapi.ElectionResult{
		Status: oapi.ElectionResultStatusPublished, Mode: &mode, Lists: &lists,
		BlankVotes: &res.Blank, Voted: &res.Voted, Electorate: &res.Electorate, Outcome: &outcome,
	}
	if res.Winner != nil {
		out.Winner = &oapi.ElectionResultWinner{Id: res.Winner.ID, Name: res.Winner.Name}
	}
	return out
}
