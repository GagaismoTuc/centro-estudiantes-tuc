package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

func (s *server) PostMemberInvitation(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.roster.Authorize(r.Context(), sess); err != nil {
		writeRosterError(w, "authorize invitation", err)
		return
	}
	var req oapi.PostMemberInvitationJSONBody
	if !decodeStrict(w, r, &req) {
		return
	}
	email, valid := claimEmail(req.Email)
	if !valid {
		writeError(w, http.StatusUnprocessableEntity, "invalid_email")
		return
	}
	if req.Role != "veedor" && req.Role != "school_admin" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_role")
		return
	}
	ip, address := s.ipKey(r), "email:"+auth.NormalizeEmail(email)
	if !s.mailLimiter.Take(ip, address) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	token, err := s.roster.Invite(r.Context(), sess, email, req.Role)
	if err != nil {
		s.mailLimiter.Refund(ip)
		s.mailLimiter.Refund(address)
		switch {
		case errors.Is(err, roster.ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, auth.ErrMFARequired):
			writeError(w, http.StatusForbidden, "mfa_required")
		default:
			slog.Error("issue invitation failed")
			writeError(w, http.StatusInternalServerError, "internal")
		}
		return
	}
	msg := mail.Message{To: email, Subject: "Invitaci\u00f3n a Centro de Estudiantes", Text: fmt.Sprintf("Hola:\n\nAbr\u00ed este enlace para aceptar la invitaci\u00f3n (vence en 48 horas):\n\n%s/invitacion#token=%s\n", s.baseURL, token)}
	w.WriteHeader(http.StatusAccepted)
	s.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.mail.Send(ctx, msg); err != nil {
			slog.Error("send invitation mail failed")
		}
	})
}

func (s *server) PostAuthInvitationAccept(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req oapi.PostAuthInvitationAcceptJSONBody
	if !decodeStrict(w, r, &req) {
		return
	}
	key := s.ipKey(r)
	if !s.tokenLimiter.Take(key) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	var password string
	if req.Password != nil {
		password = *req.Password
	}
	sess, _ := auth.SessionFromContext(r.Context())
	var previous string
	if c, err := r.Cookie(sessionCookie); err == nil {
		previous = c.Value
	}
	result, created, err := s.roster.AcceptInvitation(r.Context(), req.Token, password, previous, sess)
	if !errors.Is(err, auth.ErrInvalidToken) {
		s.tokenLimiter.Refund(key)
	}
	switch {
	case err == nil:
		s.setSessionCookieUntil(w, result.Token, result.Expires)
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		s.writeClaimMe(w, r, result, status)
	case errors.Is(err, auth.ErrInvalidToken):
		writeError(w, http.StatusUnprocessableEntity, "invalid_token")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusUnprocessableEntity, "weak_password")
	case errors.Is(err, roster.ErrInvitationLogin):
		writeError(w, http.StatusUnauthorized, "login_required")
	case errors.Is(err, auth.ErrMFARequired):
		writeError(w, http.StatusForbidden, "mfa_required")
	case errors.Is(err, auth.ErrNoSession):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	default:
		slog.Error("accept invitation failed")
		writeError(w, http.StatusInternalServerError, "internal")
	}
}
