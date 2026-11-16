package httpapi

import (
	"errors"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

type listCandidateBody struct {
	Position       *string `json:"position"`
	RosterMemberID *string `json:"rosterMemberId"`
}

type listBody struct {
	Name       *string              `json:"name"`
	Candidates *[]listCandidateBody `json:"candidates"`
}

type commissionListBody struct {
	Name                         *string              `json:"name"`
	RepresentativeRosterMemberID *string              `json:"representativeRosterMemberId"`
	Candidates                   *[]listCandidateBody `json:"candidates"`
}

type rejectListBody struct {
	Reason *string `json:"reason"`
}

func (b listBody) toInput() elections.ListInput {
	in := elections.ListInput{}
	if b.Name != nil {
		in.Name = *b.Name
	}
	if b.Candidates != nil {
		in.Candidates = make([]elections.CandidateInput, len(*b.Candidates))
		for i, c := range *b.Candidates {
			var pos, id string
			if c.Position != nil {
				pos = *c.Position
			}
			if c.RosterMemberID != nil {
				id = *c.RosterMemberID
			}
			in.Candidates[i] = elections.CandidateInput{Position: pos, RosterMemberID: id}
		}
	}
	return in
}

func electionListJSON(l elections.ElectionList) oapi.ElectionList {
	cands := make([]oapi.ListCandidate, len(l.Candidates))
	for i, c := range l.Candidates {
		cands[i] = oapi.ListCandidate{
			Position:       c.Position,
			RosterMemberId: c.RosterMemberID,
			FullName:       c.FullName,
		}
	}
	out := oapi.ElectionList{
		Id:                           l.ID,
		ElectionId:                   l.ElectionID,
		Name:                         l.Name,
		Status:                       oapi.ElectionListStatus(l.Status),
		RejectionReason:              l.RejectionReason,
		RepresentativeRosterMemberId: l.RepresentativeRosterMemberID,
		RepresentativeFullName:       l.RepresentativeFullName,
		Candidates:                   cands,
		CreatedAt:                    l.CreatedAt.UTC(),
		UpdatedAt:                    l.UpdatedAt.UTC(),
	}
	if l.ReviewedAt != nil {
		t := l.ReviewedAt.UTC()
		out.ReviewedAt = &t
	}
	return out
}

func writeListError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, elections.ErrInvalidName):
		writeError(w, http.StatusUnprocessableEntity, "invalid_name")
	case errors.Is(err, elections.ErrInvalidCandidates):
		writeError(w, http.StatusUnprocessableEntity, "invalid_candidates")
	case errors.Is(err, elections.ErrCandidateInCommission):
		writeError(w, http.StatusUnprocessableEntity, "candidate_in_commission")
	case errors.Is(err, elections.ErrInvalidRepresentative):
		writeError(w, http.StatusUnprocessableEntity, "invalid_representative")
	case errors.Is(err, elections.ErrReasonRequired):
		writeError(w, http.StatusUnprocessableEntity, "reason_required")
	case errors.Is(err, elections.ErrSubmissionClosed):
		writeError(w, http.StatusConflict, "submission_closed")
	case errors.Is(err, elections.ErrListFrozen):
		writeError(w, http.StatusConflict, "list_frozen")
	case errors.Is(err, elections.ErrListRejected):
		writeError(w, http.StatusConflict, "list_rejected")
	case errors.Is(err, elections.ErrListNameTaken):
		writeError(w, http.StatusConflict, "list_name_taken")
	case errors.Is(err, elections.ErrCandidateOnOtherList):
		writeError(w, http.StatusConflict, "candidate_on_other_list")
	default:
		writeElectionsError(w, op, err)
	}
}

func (s *server) GetElectionCandidates(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	members, err := s.elections.ListRosterCandidates(r.Context(), sess, electionID)
	if err != nil {
		writeListError(w, "election candidates", err)
		return
	}
	writeJSON(w, http.StatusOK, commissionMembersJSON(members))
}

func (s *server) GetElectionLists(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	view, err := s.elections.ListLists(r.Context(), sess, electionID)
	if err != nil {
		writeListError(w, "list election lists", err)
		return
	}
	lists := make([]oapi.ElectionList, len(view.Lists))
	for i, l := range view.Lists {
		lists[i] = electionListJSON(l)
	}
	writeJSON(w, http.StatusOK, oapi.ElectionListsResponse{
		RosterMemberId: view.RosterMemberID,
		Lists:          lists,
	})
}

func (s *server) PostElectionList(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.elections.AuthorizeSubmitList(r.Context(), sess); err != nil {
		writeListError(w, "submit election list", err)
		return
	}
	var body listBody
	if !decodeStrict(w, r, &body) {
		return
	}
	l, err := s.elections.SubmitList(r.Context(), sess, electionID, body.toInput())
	if err != nil {
		writeListError(w, "submit election list", err)
		return
	}
	writeJSON(w, http.StatusCreated, electionListJSON(l))
}

func (s *server) PostCommissionElectionList(w http.ResponseWriter, r *http.Request, electionID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.elections.AuthorizeDraft(r.Context(), sess); err != nil {
		writeListError(w, "commission load election list", err)
		return
	}
	var body commissionListBody
	if !decodeStrict(w, r, &body) {
		return
	}
	var repID string
	if body.RepresentativeRosterMemberID != nil {
		repID = *body.RepresentativeRosterMemberID
	}
	in := listBody{Name: body.Name, Candidates: body.Candidates}.toInput()
	l, err := s.elections.CommissionSubmitList(r.Context(), sess, electionID, repID, in)
	if err != nil {
		writeListError(w, "commission load election list", err)
		return
	}
	writeJSON(w, http.StatusCreated, electionListJSON(l))
}

func (s *server) GetElectionList(w http.ResponseWriter, r *http.Request, electionID, listID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	l, err := s.elections.GetList(r.Context(), sess, electionID, listID)
	if err != nil {
		writeListError(w, "get election list", err)
		return
	}
	writeJSON(w, http.StatusOK, electionListJSON(l))
}

func (s *server) PutElectionList(w http.ResponseWriter, r *http.Request, electionID, listID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.elections.AuthorizeEditList(r.Context(), sess, electionID, listID); err != nil {
		writeListError(w, "edit election list", err)
		return
	}
	var body listBody
	if !decodeStrict(w, r, &body) {
		return
	}
	l, err := s.elections.EditList(r.Context(), sess, electionID, listID, body.toInput())
	if err != nil {
		writeListError(w, "edit election list", err)
		return
	}
	writeJSON(w, http.StatusOK, electionListJSON(l))
}

func (s *server) PostApproveElectionList(w http.ResponseWriter, r *http.Request, electionID, listID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	l, err := s.elections.ApproveList(r.Context(), sess, electionID, listID)
	if err != nil {
		writeListError(w, "approve election list", err)
		return
	}
	writeJSON(w, http.StatusOK, electionListJSON(l))
}

func (s *server) PostRejectElectionList(w http.ResponseWriter, r *http.Request, electionID, listID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.elections.AuthorizeDraft(r.Context(), sess); err != nil {
		writeListError(w, "reject election list", err)
		return
	}
	var body rejectListBody
	if !decodeStrict(w, r, &body) {
		return
	}
	var reason string
	if body.Reason != nil {
		reason = *body.Reason
	}
	l, err := s.elections.RejectList(r.Context(), sess, electionID, listID, reason)
	if err != nil {
		writeListError(w, "reject election list", err)
		return
	}
	writeJSON(w, http.StatusOK, electionListJSON(l))
}
