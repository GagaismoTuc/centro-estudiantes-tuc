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

func writeFinanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, finance.ErrForbidden):
		writeError(w, 403, "forbidden")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, 403, "mfa_required")
	case errors.Is(err, finance.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, finance.ErrAmount):
		writeError(w, 422, "invalid_amount")
	case errors.Is(err, finance.ErrDate):
		writeError(w, 422, "invalid_date")
	case errors.Is(err, finance.ErrFutureDate):
		writeError(w, 422, "future_date")
	case errors.Is(err, finance.ErrDescription):
		writeError(w, 422, "description_required")
	case errors.Is(err, finance.ErrDescriptionLong):
		writeError(w, 422, "description_too_long")
	case errors.Is(err, finance.ErrCategory):
		writeError(w, 422, "invalid_category")
	case errors.Is(err, finance.ErrCategoryName):
		writeError(w, 422, "name_required")
	case errors.Is(err, finance.ErrCategoryExists):
		writeError(w, 409, "category_exists")
	case errors.Is(err, finance.ErrCategoryInUse):
		writeError(w, 409, "category_in_use")
	case errors.Is(err, finance.ErrReservedCategory):
		writeError(w, 409, "reserved_category")
	case errors.Is(err, finance.ErrReserved):
		writeError(w, 422, "reserved_category")
	case errors.Is(err, finance.ErrAlreadyReversed):
		writeError(w, 409, "already_reversed")
	case errors.Is(err, finance.ErrReverseReversal):
		writeError(w, 409, "cannot_reverse_reversal")
	case errors.Is(err, finance.ErrEmptyFile):
		writeError(w, 422, "empty_file")
	case errors.Is(err, finance.ErrFileTooLarge):
		writeError(w, 413, "file_too_large")
	case errors.Is(err, finance.ErrUnsupportedMediaType):
		writeError(w, 415, "unsupported_media_type")
	default:
		// Database errors can contain submitted values; never write them to logs.
		slog.Error("finance operation failed")
		writeError(w, 500, "internal")
	}
}
func financeEntry(e finance.Entry) oapi.FinanceEntry {
	optional := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	out := oapi.FinanceEntry{Id: e.ID, CategoryId: e.CategoryID, Direction: e.Direction, Date: e.Date, Description: e.Description, AmountCents: e.AmountCents, RecordedBy: e.RecordedBy, RecordedAt: e.RecordedAt,
		HasReceipt: e.HasReceipt, ReversesId: optional(e.ReversesID), ReversalId: optional(e.ReversalID), ReversedBy: optional(e.ReversedBy), ReversedOn: optional(e.ReversedOn)}
	if e.RosterMemberID != "" {
		out.RosterMember = &oapi.FinanceEntryRosterMember{Id: e.RosterMemberID, FullName: e.RosterMemberName}
	}
	return out
}
func (s *server) GetFinanceCategories(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	cats, err := s.finance.Categories(r.Context(), sess)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	out := make([]oapi.FinanceCategory, 0, len(cats))
	for _, c := range cats {
		out = append(out, oapi.FinanceCategory{Id: c.ID, Name: c.Name, Direction: c.Direction, Active: c.Active, Reserved: c.Reserved})
	}
	writeJSON(w, 200, out)
}
func (s *server) GetFinanceBalance(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	balance, err := s.finance.Balance(r.Context(), sess)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	writeJSON(w, 200, struct {
		BalanceCents json.Number `json:"balanceCents"`
	}{BalanceCents: json.Number(balance)})
}
func (s *server) GetFinanceCalculatorSummary(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	summary, err := s.finance.CalculatorSummary(r.Context(), sess)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		RosterCount int         `json:"rosterCount"`
		Balance     json.Number `json:"balance"`
		OwedOnDues  json.Number `json:"owedOnDues"`
	}{
		RosterCount: summary.RosterCount,
		Balance:     json.Number(summary.Balance),
		OwedOnDues:  json.Number(summary.OwedOnDues),
	})
}
func (s *server) GetFinanceEntries(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	entries, err := s.finance.List(r.Context(), sess)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	out := make([]oapi.FinanceEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, financeEntry(e))
	}
	writeJSON(w, 200, out)
}
func (s *server) GetFinanceEntry(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	entry, err := s.finance.Get(r.Context(), sess, id)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	writeJSON(w, 200, financeEntry(entry))
}
func (s *server) PostFinanceEntries(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.finance.Authorize(r.Context(), sess); err != nil {
		writeFinanceError(w, err)
		return
	}
	var req struct {
		CategoryID  string          `json:"categoryId"`
		Direction   string          `json:"direction"`
		Date        string          `json:"date"`
		Amount      json.RawMessage `json:"amountCents"`
		Description string          `json:"description"`
	}
	if !decodeStrict(w, r, &req) {
		return
	}
	var amount int64
	if len(req.Amount) == 0 || string(req.Amount) == "null" || json.Unmarshal(req.Amount, &amount) != nil || amount <= 0 {
		writeFinanceError(w, finance.ErrAmount)
		return
	}
	entry, err := s.finance.Create(r.Context(), sess, finance.NewEntry{CategoryID: req.CategoryID, Direction: req.Direction, Date: req.Date, AmountCents: amount, Description: req.Description})
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	writeJSON(w, 201, financeEntry(entry))
}
func categoryResponse(c finance.Category) oapi.FinanceCategory {
	return oapi.FinanceCategory{Id: c.ID, Name: c.Name, Direction: c.Direction, Active: c.Active, Reserved: c.Reserved}
}
func (s *server) categoryWrite(w http.ResponseWriter, r *http.Request, id, operation string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.finance.AuthorizeCategoryWrite(r.Context(), sess); err != nil {
		writeFinanceError(w, err)
		return
	}
	var req struct {
		Name      string `json:"name"`
		Direction string `json:"direction"`
	}
	if operation == "add" {
		if !decodeStrict(w, r, &req) {
			return
		}
	} else if operation == "rename" {
		var rename struct {
			Name string `json:"name"`
		}
		if !decodeStrict(w, r, &rename) {
			return
		}
		req.Name = rename.Name
	}
	var cat finance.Category
	var err error
	if operation == "add" {
		cat, err = s.finance.AddCategory(r.Context(), sess, req.Name, req.Direction)
	} else {
		cat, err = s.finance.ChangeCategory(r.Context(), sess, id, operation, req.Name)
	}
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	if operation == "delete" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	status := http.StatusOK
	if operation == "add" {
		status = http.StatusCreated
	}
	writeJSON(w, status, categoryResponse(cat))
}
func (s *server) PostFinanceCategories(w http.ResponseWriter, r *http.Request) {
	s.categoryWrite(w, r, "", "add")
}
func (s *server) PatchFinanceCategory(w http.ResponseWriter, r *http.Request, id string) {
	s.categoryWrite(w, r, id, "rename")
}
func (s *server) DeleteFinanceCategory(w http.ResponseWriter, r *http.Request, id string) {
	s.categoryWrite(w, r, id, "delete")
}
func (s *server) PostFinanceCategoryArchive(w http.ResponseWriter, r *http.Request, id string) {
	s.categoryWrite(w, r, id, "archive")
}

func (s *server) PostFinanceEntryReversal(w http.ResponseWriter, r *http.Request, entryID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	entry, err := s.finance.Reverse(r.Context(), sess, entryID)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	writeJSON(w, 201, financeEntry(entry))
}
