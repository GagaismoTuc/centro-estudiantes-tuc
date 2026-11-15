package httpapi

import (
	"errors"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

func (s *server) GetMembers(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	rows, err := s.roster.ListMembers(r.Context(), sess)
	if err != nil {
		writeRosterError(w, "list members", err)
		return
	}
	out := make([]oapi.SchoolMember, 0, len(rows))
	for _, row := range rows {
		roles := make([]oapi.Role, len(row.Roles))
		for i, role := range row.Roles {
			roles[i] = oapi.Role(role)
		}
		out = append(out, oapi.SchoolMember{Email: row.Email, FullName: row.FullName, RosterMemberId: row.RosterMemberID, Position: row.Position, Roles: roles})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) PostMemberOfficer(w http.ResponseWriter, r *http.Request, memberID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.roster.Authorize(r.Context(), sess); err != nil {
		writeRosterError(w, "assign officer", err)
		return
	}
	var req oapi.PostMemberOfficerJSONBody
	if !decodeStrict(w, r, &req) {
		return
	}
	err := s.roster.AssignOfficer(r.Context(), sess, memberID, req.Position)
	switch {
	case errors.Is(err, roster.ErrAlreadyOfficer):
		writeError(w, http.StatusConflict, "already_officer")
	case errors.Is(err, roster.ErrPositionTaken):
		writeError(w, http.StatusConflict, "position_taken")
	case errors.Is(err, roster.ErrInvalidPosition):
		writeError(w, http.StatusUnprocessableEntity, "invalid_position")
	case err != nil:
		writeRosterError(w, "assign officer", err)
	default:
		w.WriteHeader(http.StatusCreated)
	}
}

func (s *server) DeleteMemberOfficer(w http.ResponseWriter, r *http.Request, memberID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.roster.RemoveOfficer(r.Context(), sess, memberID); err != nil {
		writeRosterError(w, "remove officer", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
