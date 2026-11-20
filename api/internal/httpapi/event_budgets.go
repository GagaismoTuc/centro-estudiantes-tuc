package httpapi

import (
	"errors"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/budgets"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

func writeBudgetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, budgets.ErrForbidden):
		writeError(w, 403, "forbidden")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, 403, "mfa_required")
	case errors.Is(err, budgets.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, budgets.ErrArchived):
		writeError(w, 409, "budget_archived")
	case errors.Is(err, budgets.ErrHasEntries):
		writeError(w, 409, "budget_has_entries")
	case errors.Is(err, budgets.ErrQuantity):
		writeError(w, 422, "invalid_quantity")
	case errors.Is(err, budgets.ErrAmount):
		writeError(w, 422, "invalid_amount")
	case errors.Is(err, budgets.ErrDay):
		writeError(w, 422, "invalid_day")
	case errors.Is(err, budgets.ErrKind):
		writeError(w, 422, "invalid_kind")
	case errors.Is(err, budgets.ErrNameRequired):
		writeError(w, 422, "name_required")
	case errors.Is(err, budgets.ErrInvalidRange):
		writeError(w, 422, "invalid_range")
	default:
		writeError(w, 500, "internal")
	}
}
func budgetDTO(b budgets.Budget) oapi.EventBudget {
	return oapi.EventBudget{Id: b.ID, Name: b.Name, StartsOn: b.StartsOn, EndsOn: b.EndsOn, Archived: b.Archived}
}
func (s *server) PostEventBudget(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.budgets.AuthorizeWrite(r.Context(), sess); err != nil {
		writeBudgetError(w, err)
		return
	}
	var req oapi.PostEventBudgetJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	b, err := s.budgets.Create(r.Context(), sess, req.Name, req.StartsOn, req.EndsOn)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, budgetDTO(b))
}

func (s *server) PostEventBudgetLine(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.budgets.AuthorizeWrite(r.Context(), sess); err != nil {
		writeBudgetError(w, err)
		return
	}
	var req oapi.PostEventBudgetLineJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	line, err := s.budgets.AddLine(r.Context(), sess, id, budgets.LineInput{Kind: req.Kind, Description: req.Description, HeadingID: req.HeadingId, ContactID: req.ContactId, ReferencePriceID: req.ReferencePriceId, Quantity: req.Quantity, UnitAmountCents: req.UnitAmountCents, Day: req.Day})
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, 201, oapi.EventBudgetLine{Id: line.ID, Kind: line.Kind, Description: line.Description, HeadingId: line.HeadingID, ContactId: line.ContactID, Quantity: line.Quantity, UnitAmountCents: line.UnitAmountCents, Day: line.Day})
}

func budgetLineDTO(line budgets.Line) oapi.EventBudgetLine {
	return oapi.EventBudgetLine{Id: line.ID, Kind: line.Kind, Description: line.Description, HeadingId: line.HeadingID, ContactId: line.ContactID, Quantity: line.Quantity, UnitAmountCents: line.UnitAmountCents, Day: line.Day}
}
func budgetTotalsDTO(t budgets.Totals) oapi.EventBudgetTotals {
	return oapi.EventBudgetTotals{CostCents: t.Cost, IncomeCents: t.Income, ResultCents: t.Result, BreakEvenCents: t.BreakEven}
}
func (s *server) GetEventBudget(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	b, err := s.budgets.Get(r.Context(), sess, id)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	lines := make([]oapi.EventBudgetLine, 0, len(b.Lines))
	for _, v := range b.Lines {
		lines = append(lines, budgetLineDTO(v))
	}
	days := make([]oapi.EventBudgetDay, 0, len(b.Days))
	for _, v := range b.Days {
		days = append(days, oapi.EventBudgetDay{Day: v.Day, Totals: budgetTotalsDTO(v.Totals)})
	}
	writeJSON(w, 200, oapi.EventBudgetDetail{Id: b.ID, Name: b.Name, StartsOn: b.StartsOn, EndsOn: b.EndsOn, Archived: b.Archived, Lines: lines, Totals: budgetTotalsDTO(b.Totals), Days: days})
}

func (s *server) DeleteEventBudget(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.budgets.Delete(r.Context(), sess, id); err != nil {
		writeBudgetError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) PostEventBudgetArchive(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	b, err := s.budgets.Archive(r.Context(), sess, id)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, 200, budgetDTO(b))
}

func (s *server) PutEventBudgetLine(w http.ResponseWriter, r *http.Request, id, lineID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.budgets.AuthorizeWrite(r.Context(), sess); err != nil {
		writeBudgetError(w, err)
		return
	}
	var req oapi.PutEventBudgetLineJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	line, err := s.budgets.UpdateLine(r.Context(), sess, id, lineID, budgets.LineInput{Kind: req.Kind, Description: req.Description, HeadingID: req.HeadingId, ContactID: req.ContactId, ReferencePriceID: req.ReferencePriceId, Quantity: req.Quantity, UnitAmountCents: req.UnitAmountCents, Day: req.Day})
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, 200, budgetLineDTO(line))
}

func (s *server) GetEventBudgets(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	rows, err := s.budgets.List(r.Context(), sess)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	out := make([]oapi.EventBudget, 0, len(rows))
	for _, b := range rows {
		out = append(out, budgetDTO(b))
	}
	writeJSON(w, 200, out)
}

func (s *server) PutEventBudget(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.budgets.AuthorizeWrite(r.Context(), sess); err != nil {
		writeBudgetError(w, err)
		return
	}
	var req oapi.PutEventBudgetJSONRequestBody
	if !decodeStrict(w, r, &req) {
		return
	}
	b, err := s.budgets.Update(r.Context(), sess, id, req.Name, req.StartsOn, req.EndsOn)
	if err != nil {
		writeBudgetError(w, err)
		return
	}
	writeJSON(w, 200, budgetDTO(b))
}

func (s *server) DeleteEventBudgetLine(w http.ResponseWriter, r *http.Request, id, lineID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.budgets.DeleteLine(r.Context(), sess, id, lineID); err != nil {
		writeBudgetError(w, err)
		return
	}
	w.WriteHeader(204)
}
