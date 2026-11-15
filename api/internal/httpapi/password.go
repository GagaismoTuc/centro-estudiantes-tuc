package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// PostAuthPassword changes the signed-in user's password (see openapi.yaml).
func (s *server) PostAuthPassword(w http.ResponseWriter, r *http.Request) {
	sess, token, ok := mfaSession(w, r)
	if !ok {
		return
	}
	role, _, err := s.store.Membership(r.Context(), sess)
	if err != nil {
		slog.Error("change password: membership", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	if auth.Privileged(role) && auth.RequireMFA(sess) != nil {
		writeError(w, http.StatusForbidden, "mfa_required")
		return
	}
	var req oapi.PasswordChangeRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	// Checked before the limiter: a weak new password is not a guess of the current one.
	if auth.ValidatePassword(req.NewPassword) != nil {
		writeError(w, http.StatusUnprocessableEntity, "weak_password")
		return
	}
	// The sign-in budget of the user's email: a wrong current password is a failed sign-in.
	key := "email:" + auth.NormalizeEmail(sess.Email)
	if !s.limiter.Take(key) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	tok, expires, err := s.store.ChangePassword(r.Context(), token, sess.UserID, req.CurrentPassword, req.NewPassword)
	switch {
	case err == nil:
		// As after a sign-in: proving the password clears the email's failures.
		s.limiter.Reset(key)
		s.setSessionCookieUntil(w, tok, expires)
		w.WriteHeader(http.StatusNoContent)
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		// The only outcome that keeps the attempt.
		writeError(w, http.StatusUnprocessableEntity, "invalid_credentials")
		return
	}
	s.limiter.Refund(key)
	switch {
	case errors.Is(err, auth.ErrSamePassword):
		writeError(w, http.StatusUnprocessableEntity, "same_password")
	case errors.Is(err, auth.ErrNoSession):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	default:
		slog.Error("change password", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}
