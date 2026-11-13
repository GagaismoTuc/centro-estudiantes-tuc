package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

const maxImportBytes = 1 << 20

// rosterSession marks the response as not cacheable (personal data, one-time codes) and returns
// the session, or answers 401. The school_admin check happens in the roster store.
func rosterSession(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := auth.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	}
	return sess, ok
}

func writeRosterError(w http.ResponseWriter, op string, err error) {
	var invalid *roster.InvalidRowsError
	switch {
	case errors.Is(err, roster.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, http.StatusForbidden, "mfa_required")
	case errors.Is(err, roster.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, roster.ErrHasAccount):
		writeError(w, http.StatusConflict, "member_has_account")
	case errors.Is(err, roster.ErrTooManyRows):
		writeError(w, http.StatusUnprocessableEntity, "too_many_rows")
	case errors.As(err, &invalid):
		rows := make([]oapi.RowError, len(invalid.Rows))
		for i, r := range invalid.Rows {
			rows[i] = oapi.RowError{Line: r.Line, Reason: r.Reason}
		}
		writeJSON(w, http.StatusUnprocessableEntity, oapi.ImportError{Error: "invalid_rows", Rows: &rows})
	default:
		slog.Error(op, "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func (s *server) GetRoster(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	members, err := s.roster.List(r.Context(), sess)
	if err != nil {
		writeRosterError(w, "list roster", err)
		return
	}
	out := make([]oapi.RosterMember, len(members))
	for i, m := range members {
		out[i] = oapi.RosterMember{Id: m.ID, FullName: m.FullName, DocumentNumber: m.DocumentNumber,
			HasActiveCode: m.HasActiveCode, HasAccount: m.HasAccount}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) PostRosterImport(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	// Refuse before reading up to 1 MiB from someone who may not import.
	if err := s.roster.Authorize(r.Context(), sess); err != nil {
		writeRosterError(w, "import roster", err)
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "text/csv" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	inserted, skipped, err := s.roster.Import(r.Context(), sess, data)
	if err != nil {
		writeRosterError(w, "import roster", err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.ImportResult{Inserted: inserted, Skipped: skipped})
}

func (s *server) PostRosterMemberAccessCode(w http.ResponseWriter, r *http.Request, memberID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	c, err := s.roster.Issue(r.Context(), sess, memberID)
	if err != nil {
		writeRosterError(w, "issue access code", err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.IssuedCode{Code: c.Code, ExpiresAt: c.ExpiresAt})
}

func (s *server) PostRosterAccessCodes(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	codes, err := s.roster.IssueMissing(r.Context(), sess)
	if err != nil {
		writeRosterError(w, "issue access codes", err)
		return
	}
	out := make([]oapi.IssuedMemberCode, len(codes))
	for i, c := range codes {
		out[i] = oapi.IssuedMemberCode{MemberId: c.MemberID, FullName: c.FullName, DocumentNumber: c.DocumentNumber,
			Code: c.Code, ExpiresAt: c.ExpiresAt}
	}
	writeJSON(w, http.StatusCreated, out)
}
