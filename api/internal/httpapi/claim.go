package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

const maxEmail = 254

// decodeStrict reads a JSON body of at most 4 KiB into v, rejecting unknown fields and trailing data.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

// claimEmail returns the stored form of a new account's email, or false when it is not a bare address.
func claimEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s || len(s) > maxEmail {
		return "", false
	}
	return auth.NormalizeEmail(s), true
}

// spendCode applies the per-IP limit to one code attempt: only invalid_code outcomes keep the hit.
// It writes every error response and returns the result on success.
func (s *server) spendCode(w http.ResponseWriter, r *http.Request, use func() (roster.Claimed, error)) (roster.Claimed, bool) {
	key := s.ipKey(r)
	if !s.claimLimiter.Take(key) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return roster.Claimed{}, false
	}
	c, err := use()
	// email_in_use keeps the hit too: otherwise one valid code allows unlimited probing of registered emails.
	if !errors.Is(err, roster.ErrInvalidCode) && !errors.Is(err, auth.ErrEmailTaken) {
		s.claimLimiter.Refund(key)
	}
	switch {
	case err == nil:
		return c, true
	case errors.Is(err, roster.ErrInvalidCode):
		writeError(w, http.StatusUnprocessableEntity, "invalid_code")
	case errors.Is(err, auth.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_in_use")
	case errors.Is(err, roster.ErrAlreadyMember):
		writeError(w, http.StatusConflict, "already_member")
	case errors.Is(err, auth.ErrNoSession):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	default:
		slog.Error("use access code", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
	return roster.Claimed{}, false
}

// studentMe is the session after a claim or redeem: a student in the code's tenant, so no second
// factor is required there.
func studentMe(c roster.Claimed, email string, verified bool) oapi.Me {
	role := oapi.Student
	return oapi.Me{UserId: c.UserID, Email: email, EmailVerified: verified, TenantId: &c.TenantID, TenantName: &c.TenantName, Role: &role,
		Mfa: oapi.MfaState(auth.MFANotRequired)}
}

func (s *server) PostAuthClaim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req oapi.ClaimRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	email, ok := claimEmail(req.Email)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_email")
		return
	}
	if auth.ValidatePassword(req.Password) != nil {
		writeError(w, http.StatusUnprocessableEntity, "weak_password")
		return
	}
	// The new session is created with the account and the one the request came with goes away in the
	// same transaction.
	var prev string
	if _, ok := auth.SessionFromContext(r.Context()); ok {
		c, _ := r.Cookie(sessionCookie) // present: withSession resolved it
		prev = c.Value
	}
	c, ok := s.spendCode(w, r, func() (roster.Claimed, error) {
		return s.roster.Claim(r.Context(), req.Code, email, req.Password, prev)
	})
	if !ok {
		return
	}
	s.setSessionCookieUntil(w, c.Token, c.Expires)
	writeJSON(w, http.StatusCreated, studentMe(c, email, false))
	s.sendLink(auth.PurposeVerifyEmail, email, c.VerifyToken)
}

func (s *server) PostAuthRedeem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := auth.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req oapi.RedeemRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	cookie, _ := r.Cookie(sessionCookie) // present: withSession resolved it
	// Joining a school changes what the session may do: Redeem rotates it (new token, same absolute
	// expiry) in the same transaction.
	c, ok := s.spendCode(w, r, func() (roster.Claimed, error) {
		return s.roster.Redeem(r.Context(), sess, cookie.Value, req.Code)
	})
	if !ok {
		return
	}
	s.setSessionCookieUntil(w, c.Token, c.Expires)
	writeJSON(w, http.StatusOK, studentMe(c, sess.Email, sess.EmailVerified))
}
