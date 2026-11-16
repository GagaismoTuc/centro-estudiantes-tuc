package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

func writeDueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, finance.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, http.StatusForbidden, "mfa_required")
	case errors.Is(err, finance.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, finance.ErrDueName):
		writeError(w, http.StatusUnprocessableEntity, "name_required")
	case errors.Is(err, finance.ErrAmount):
		writeError(w, http.StatusUnprocessableEntity, "invalid_amount")
	case errors.Is(err, finance.ErrDate):
		writeError(w, http.StatusUnprocessableEntity, "invalid_date")
	case errors.Is(err, finance.ErrEmptyRoster):
		writeError(w, http.StatusUnprocessableEntity, "empty_roster")
	case errors.Is(err, finance.ErrDueFrozen):
		writeError(w, http.StatusConflict, "due_frozen")
	default:
		// Never log database errors that may echo submitted names or amounts.
		slog.Error("finance dues operation failed")
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func dueSummaryResponse(d finance.Due) oapi.FinanceDueSummary {
	return oapi.FinanceDueSummary{
		Id:          d.ID,
		Name:        d.Name,
		AmountCents: d.AmountCents,
		DueDate:     d.DueDate,
		CreatedAt:   d.CreatedAt,
		OwingCount:  d.OwingCount,
		TotalCount:  d.TotalCount,
	}
}

func dueDetailResponse(d finance.DueDetail) oapi.FinanceDueDetail {
	members := make([]oapi.FinanceDueMember, 0, len(d.Members))
	for _, m := range d.Members {
		members = append(members, oapi.FinanceDueMember{
			RosterMemberId: m.RosterMemberID,
			FullName:       m.FullName,
			Status:         oapi.FinanceDueStatus(m.Status),
		})
	}
	return oapi.FinanceDueDetail{
		Id:          d.ID,
		Name:        d.Name,
		AmountCents: d.AmountCents,
		DueDate:     d.DueDate,
		CreatedAt:   d.CreatedAt,
		OwingCount:  d.OwingCount,
		TotalCount:  d.TotalCount,
		Members:     members,
	}
}

func (s *server) GetFinanceDues(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	dues, err := s.finance.ListDues(r.Context(), sess)
	if err != nil {
		writeDueError(w, err)
		return
	}
	out := make([]oapi.FinanceDueSummary, 0, len(dues))
	for _, d := range dues {
		out = append(out, dueSummaryResponse(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func decodePositiveAmount(w http.ResponseWriter, raw json.RawMessage) (int64, bool) {
	var amount int64
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &amount) != nil || amount <= 0 {
		writeDueError(w, finance.ErrAmount)
		return 0, false
	}
	return amount, true
}

func (s *server) PostFinanceDues(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.finance.AuthorizeDueWrite(r.Context(), sess); err != nil {
		writeDueError(w, err)
		return
	}
	var req struct {
		Name        string          `json:"name"`
		AmountCents json.RawMessage `json:"amountCents"`
		DueDate     string          `json:"dueDate"`
	}
	if !decodeStrict(w, r, &req) {
		return
	}
	amount, ok := decodePositiveAmount(w, req.AmountCents)
	if !ok {
		return
	}
	due, err := s.finance.CreateDue(r.Context(), sess, finance.NewDue{
		Name:        req.Name,
		AmountCents: amount,
		DueDate:     req.DueDate,
	})
	if err != nil {
		writeDueError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, dueDetailResponse(due))
}

func (s *server) GetMyFinanceDues(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	dues, err := s.finance.ListMyDues(r.Context(), sess)
	if err != nil {
		writeDueError(w, err)
		return
	}
	out := make([]oapi.MyFinanceDue, 0, len(dues))
	for _, d := range dues {
		out = append(out, oapi.MyFinanceDue{
			Id:          d.ID,
			Name:        d.Name,
			AmountCents: d.AmountCents,
			DueDate:     d.DueDate,
			Status:      oapi.FinanceDueStatus(d.Status),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) GetFinanceDue(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	due, err := s.finance.GetDue(r.Context(), sess, id)
	if err != nil {
		writeDueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dueDetailResponse(due))
}

func decodeOptionalString(w http.ResponseWriter, raw json.RawMessage, nullErr error) (*string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	if string(raw) == "null" {
		writeDueError(w, nullErr)
		return nil, false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return nil, false
	}
	return &value, true
}

func (s *server) PatchFinanceDue(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.finance.AuthorizeDueWrite(r.Context(), sess); err != nil {
		writeDueError(w, err)
		return
	}
	var req struct {
		Name        json.RawMessage `json:"name"`
		AmountCents json.RawMessage `json:"amountCents"`
		DueDate     json.RawMessage `json:"dueDate"`
	}
	if !decodeStrict(w, r, &req) {
		return
	}
	name, ok := decodeOptionalString(w, req.Name, finance.ErrDueName)
	if !ok {
		return
	}
	dueDate, ok := decodeOptionalString(w, req.DueDate, finance.ErrDate)
	if !ok {
		return
	}
	var in = finance.UpdateDue{Name: name, DueDate: dueDate}
	if len(req.AmountCents) > 0 {
		amount, ok := decodePositiveAmount(w, req.AmountCents)
		if !ok {
			return
		}
		in.AmountCents = &amount
	}
	due, err := s.finance.UpdateDue(r.Context(), sess, id, in)
	if err != nil {
		writeDueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dueDetailResponse(due))
}

func (s *server) DeleteFinanceDue(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.finance.DeleteDue(r.Context(), sess, id); err != nil {
		writeDueError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
