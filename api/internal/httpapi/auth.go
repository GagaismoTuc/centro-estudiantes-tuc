package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// The __Host- prefix makes browsers require Secure, Path=/ and no Domain, so a sibling
// subdomain cannot set or overwrite the session cookie.
const sessionCookie = "__Host-session"

func setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge, // negative deletes the cookie ("Max-Age=0")
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// withSession loads the session named by the cookie, if valid, into the request context.
func (s *server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := s.store.Lookup(r.Context(), c.Value)
		switch {
		case err == nil:
			r = r.WithContext(auth.ContextWithSession(r.Context(), sess))
		case !errors.Is(err, auth.ErrNoSession):
			slog.Error("session lookup", "err", err)
			writeError(w, http.StatusInternalServerError, "internal")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the TCP peer address.
//
// shortcut: behind a reverse proxy (Caddy) every request shares the proxy's address, so the
// per-IP limit needs trusted-proxy handling of X-Forwarded-For before deploying behind one.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) PostAuthLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req oapi.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	ipKey, emailKey := "ip:"+clientIP(r), "email:"+auth.NormalizeEmail(req.Email)
	if !s.limiter.Take(ipKey, emailKey) {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	userID, err := s.store.Authenticate(r.Context(), req.Email, req.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if err != nil {
		slog.Error("login", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	// Only failures count: a success clears the email and gives the IP its attempt back.
	s.limiter.Reset(emailKey)
	s.limiter.Refund(ipKey)
	token, err := s.store.CreateSession(r.Context(), userID)
	if err != nil {
		slog.Error("create session", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	setSessionCookie(w, token, int(auth.SessionLifetime.Seconds()))
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) PostAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(r.Context(), c.Value); err != nil {
			slog.Error("logout", "err", err)
			writeError(w, http.StatusInternalServerError, "internal")
			return
		}
	}
	setSessionCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) GetAuthMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	role, err := s.store.Role(r.Context(), sess)
	if err != nil {
		slog.Error("me", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	me := oapi.Me{UserId: sess.UserID, Email: sess.Email}
	if sess.TenantID != "" {
		me.TenantId = &sess.TenantID
	}
	if role != "" {
		r := oapi.Role(role)
		me.Role = &r
	}
	writeJSON(w, http.StatusOK, me)
}
