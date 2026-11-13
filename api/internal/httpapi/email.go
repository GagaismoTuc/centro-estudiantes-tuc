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
)

// noSender is the Sender of a router built without one: every send fails and is logged.
type noSender struct{}

func (noSender) Send(context.Context, mail.Message) error {
	return errors.New("mail: no sender configured")
}

// linkMail is the user-facing copy (Spanish) of each emailed link; %s is the link.
var linkMail = map[auth.Purpose]struct{ subject, path, text string }{
	auth.PurposeVerifyEmail: {"Confirmá tu correo", "/verificar",
		"Hola:\n\nPara confirmar tu correo en Centro de Estudiantes abrí este enlace (vence en 48 horas):\n\n%s\n\nSi no fuiste vos, ignorá este mensaje.\n"},
	auth.PurposeSignIn: {"Tu enlace para ingresar", "/ingresar",
		"Hola:\n\nPara ingresar a Centro de Estudiantes abrí este enlace (vence en 15 minutos y sirve una sola vez):\n\n%s\n\nSi no lo pediste, ignorá este mensaje.\n"},
	auth.PurposeResetPassword: {"Restablecer tu contraseña", "/restablecer",
		"Hola:\n\nPara elegir una contraseña nueva abrí este enlace (vence en 30 minutos y sirve una sola vez):\n\n%s\n\nAl cambiarla se cierran todas tus sesiones. Si no lo pediste, ignorá este mensaje.\n"},
}

// sendLink emails a link carrying token in its URL fragment (never sent to servers or in Referer).
// It runs after the business change committed; a failure is logged without the message or token.
func (s *server) sendLink(p auth.Purpose, to, token string) {
	if token == "" {
		return
	}
	t := linkMail[p]
	msg := mail.Message{To: to, Subject: t.subject, Text: fmt.Sprintf(t.text, s.baseURL+t.path+"#token="+token)}
	s.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.mail.Send(ctx, msg); err != nil {
			slog.Error("send mail", "purpose", string(p), "err", err)
		}
	})
}

// requestLink rate-limits one mail request (per IP and per email, every request counts), issues the
// token and answers 202 the same way whether or not anything is sent.
func (s *server) requestLink(w http.ResponseWriter, r *http.Request, email string, p auth.Purpose,
	issue func() (auth.Emailed, error)) {
	ipKey, emailKey := s.ipKey(r), "email:"+auth.NormalizeEmail(email)
	if !s.mailLimiter.Take(ipKey, emailKey) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	e, err := issue()
	if err != nil {
		// A server error does not spend the client's budget.
		s.mailLimiter.Refund(ipKey)
		s.mailLimiter.Refund(emailKey)
		slog.Error("issue email token", "purpose", string(p), "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusAccepted)
	s.sendLink(p, e.Email, e.Token)
}

// spendToken applies the per-IP limit to one token attempt: only invalid_token outcomes keep the hit.
func (s *server) spendToken(w http.ResponseWriter, r *http.Request, use func() error) bool {
	key := s.ipKey(r)
	if !s.tokenLimiter.Take(key) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return false
	}
	err := use()
	if !errors.Is(err, auth.ErrInvalidToken) {
		s.tokenLimiter.Refund(key)
	}
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrInvalidToken):
		writeError(w, http.StatusUnprocessableEntity, "invalid_token")
	default:
		slog.Error("use email token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
	return false
}

func (s *server) PostAuthEmailVerification(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := auth.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	s.requestLink(w, r, sess.Email, auth.PurposeVerifyEmail, func() (auth.Emailed, error) {
		return s.store.RequestVerification(r.Context(), sess.UserID)
	})
}

func (s *server) PostAuthLink(w http.ResponseWriter, r *http.Request) {
	s.requestByEmail(w, r, auth.PurposeSignIn)
}

func (s *server) PostAuthPasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	s.requestByEmail(w, r, auth.PurposeResetPassword)
}

func (s *server) requestByEmail(w http.ResponseWriter, r *http.Request, p auth.Purpose) {
	w.Header().Set("Cache-Control", "no-store")
	var req oapi.EmailRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	s.requestLink(w, r, req.Email, p, func() (auth.Emailed, error) {
		return s.store.RequestByEmail(r.Context(), req.Email, p)
	})
}

func (s *server) PostAuthEmailVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req oapi.TokenRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	if s.spendToken(w, r, func() error { return s.store.VerifyEmail(r.Context(), req.Token) }) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) PostAuthLinkConsume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req oapi.TokenRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	var user string
	if !s.spendToken(w, r, func() (err error) {
		user, err = s.store.ConsumeSignInLink(r.Context(), req.Token)
		return err
	}) {
		return
	}
	if s.startSession(w, r, user) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) PostAuthPasswordReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req oapi.PasswordResetRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	// Checked before the limiter and the token: a weak password neither counts nor consumes the token.
	if auth.ValidatePassword(req.Password) != nil {
		writeError(w, http.StatusUnprocessableEntity, "weak_password")
		return
	}
	if s.spendToken(w, r, func() error { return s.store.ResetPassword(r.Context(), req.Token, req.Password) }) {
		w.WriteHeader(http.StatusNoContent)
	}
}
