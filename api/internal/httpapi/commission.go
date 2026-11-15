package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

func writeElectionsError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, elections.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, http.StatusForbidden, "mfa_required")
	case errors.Is(err, elections.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, elections.ErrElectionFrozen):
		writeError(w, http.StatusConflict, "election_frozen")
	case errors.Is(err, elections.ErrEmptyCommission):
		writeError(w, http.StatusUnprocessableEntity, "empty_commission")
	case errors.Is(err, elections.ErrNoAccount):
		writeError(w, http.StatusUnprocessableEntity, "no_account")
	default:
		slog.Error(op, "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func commissionJSON(c elections.Commission) oapi.ElectoralCommission {
	return oapi.ElectoralCommission{Id: c.ID, CreatedAt: c.CreatedAt.UTC(), Members: commissionMembersJSON(c.Members)}
}

func commissionMembersJSON(ms []elections.CommissionMember) []oapi.CommissionMember {
	out := make([]oapi.CommissionMember, len(ms))
	for i, m := range ms {
		out[i] = oapi.CommissionMember{RosterMemberId: m.RosterMemberID, FullName: m.FullName}
	}
	return out
}

func (s *server) GetElectoralCommission(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	st, err := s.elections.Status(r.Context(), sess)
	if err != nil {
		writeElectionsError(w, "read commission", err)
		return
	}
	out := oapi.ElectoralCommissionStatus{IsMember: st.IsMember, CanDesignate: st.CanDesignate}
	if st.Commission != nil {
		c := commissionJSON(*st.Commission)
		out.Commission = &c
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) PutElectoralCommission(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	// Refuse before reading the body from someone who may not designate.
	if err := s.elections.AuthorizeDesignation(r.Context(), sess); err != nil {
		writeElectionsError(w, "designate commission", err)
		return
	}
	var req oapi.CommissionDesignation
	if !decodeStrict(w, r, &req) {
		return
	}
	var ids []string // missing or null: empty_commission
	if req.RosterMemberIds != nil {
		ids = *req.RosterMemberIds
	}
	c, err := s.elections.Designate(r.Context(), sess, ids)
	if err != nil {
		writeElectionsError(w, "designate commission", err)
		return
	}
	writeJSON(w, http.StatusOK, commissionJSON(c))
}

func (s *server) GetElectoralCommissionCandidates(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	ms, err := s.elections.Candidates(r.Context(), sess)
	if err != nil {
		writeElectionsError(w, "commission candidates", err)
		return
	}
	writeJSON(w, http.StatusOK, commissionMembersJSON(ms))
}
