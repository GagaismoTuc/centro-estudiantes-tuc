package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// mfaSession marks the response not cacheable and returns the session and its cookie token, or
// answers 401.
func mfaSession(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := auth.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return auth.Session{}, "", false
	}
	c, _ := r.Cookie(sessionCookie) // present: withSession resolved it
	return sess, c.Value, true
}

// spendMFA applies the per-user limit to one code attempt: only invalid_code outcomes keep the hit.
// It writes every error response and reports success.
func (s *server) spendMFA(w http.ResponseWriter, userID string, use func() error) bool {
	key := "user:" + userID
	if !s.mfaLimiter.Take(key) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return false
	}
	err := use()
	if !errors.Is(err, auth.ErrInvalidCode) {
		s.mfaLimiter.Refund(key)
	}
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrInvalidCode):
		writeError(w, http.StatusUnprocessableEntity, "invalid_code")
	case errors.Is(err, auth.ErrAlreadyEnrolled):
		writeError(w, http.StatusConflict, "already_enrolled")
	case errors.Is(err, auth.ErrNoSession):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	default:
		slog.Error("two-factor code", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
	return false
}

func (s *server) PostAuthMfaEnrollment(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := mfaSession(w, r)
	if !ok {
		return
	}
	secret, uri, err := s.mfa.Enroll(r.Context(), sess.UserID, sess.Email)
	if errors.Is(err, auth.ErrAlreadyEnrolled) {
		writeError(w, http.StatusConflict, "already_enrolled")
		return
	}
	if err != nil {
		slog.Error("enroll totp", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, oapi.MfaEnrollment{Secret: secret, OtpauthUri: uri})
}

func (s *server) PostAuthMfaEnrollmentConfirm(w http.ResponseWriter, r *http.Request) {
	sess, token, ok := mfaSession(w, r)
	if !ok {
		return
	}
	var req oapi.MfaCodeRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	var newToken string
	var codes []string
	if !s.spendMFA(w, sess.UserID, func() (err error) {
		newToken, codes, err = s.mfa.Confirm(r.Context(), token, sess.UserID, req.Code)
		return err
	}) {
		return
	}
	setSessionCookie(w, newToken, int(auth.SessionLifetime.Seconds()))
	writeJSON(w, http.StatusOK, oapi.RecoveryCodes{RecoveryCodes: codes})
}

func (s *server) PostAuthMfaVerify(w http.ResponseWriter, r *http.Request) {
	sess, token, ok := mfaSession(w, r)
	if !ok {
		return
	}
	var req oapi.MfaVerifyRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	if (req.Code == nil) == (req.RecoveryCode == nil) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	var code, recovery string
	if req.Code != nil {
		code = *req.Code
	} else {
		recovery = *req.RecoveryCode
	}
	var newToken string
	if !s.spendMFA(w, sess.UserID, func() (err error) {
		newToken, err = s.mfa.Verify(r.Context(), token, sess.UserID, code, recovery)
		return err
	}) {
		return
	}
	setSessionCookie(w, newToken, int(auth.SessionLifetime.Seconds()))
	w.WriteHeader(http.StatusNoContent)
}
