package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

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

// setSessionCookieUntil sets the cookie for a session expiring at expires: Max-Age is the session's
// remaining lifetime (at least 1, since 0 would make it a browser-session cookie).
func (s *server) setSessionCookieUntil(w http.ResponseWriter, token string, expires time.Time) {
	setSessionCookie(w, token, max(1, int(expires.Sub(s.now()).Seconds())))
}

// setNewSessionCookie sets the cookie for a session created now.
func (s *server) setNewSessionCookie(w http.ResponseWriter, token string) {
	s.setSessionCookieUntil(w, token, s.now().Add(auth.SessionLifetime))
}

// dropPreviousSession deletes the valid session the request arrived with, if any, so a new sign-in
// replaces it instead of leaving it alive next to the new one.
func (s *server) dropPreviousSession(r *http.Request) error {
	if _, ok := auth.SessionFromContext(r.Context()); !ok {
		return nil
	}
	c, _ := r.Cookie(sessionCookie) // present: withSession resolved it
	return s.store.DeleteSession(r.Context(), c.Value)
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

// unparsableKey is the shared limiter key of requests whose X-Forwarded-For has an unparsable hop
// before any untrusted one: they are neither keyed by the proxy (all its clients would share one
// budget) nor by a value the client could vary.
const unparsableKey = "ip:unparsable"

// ipKey is the per-IP limiter key of the request's client. The client is the TCP peer unless the
// peer is a trusted proxy: then it is the right-most X-Forwarded-For address (all headers, in order)
// that is not itself a trusted proxy, since addresses to its left can be forged by the client. When
// every hop is trusted the left-most one is the client; without the header, the peer. Empty hops are
// skipped, ports are dropped, and an unparsable hop met before any untrusted one yields
// unparsableKey. IPv6 clients are keyed by their /64, which one host usually owns.
func (s *server) ipKey(r *http.Request) string {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return "ip:" + r.RemoteAddr
	}
	client := peer.Addr().Unmap().WithZone("")
	if s.isTrusted(client) {
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			h := strings.TrimSpace(hops[i])
			if h == "" {
				continue
			}
			a, ok := parseHop(h)
			if !ok {
				return unparsableKey
			}
			client = a // the left-most hop so far, if every hop is trusted
			if !s.isTrusted(a) {
				break
			}
		}
	}
	if client.Is6() {
		return "ip:" + netip.PrefixFrom(client, 64).Masked().String()
	}
	return "ip:" + client.String()
}

// parseHop parses one X-Forwarded-For entry: an address, with or without a port (1.2.3.4:5678,
// [2001:db8::1]:443) or brackets, returned unmapped and without zone.
func parseHop(h string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(h, "["), "]"))
	if err != nil {
		ap, perr := netip.ParseAddrPort(h)
		if perr != nil {
			return netip.Addr{}, false
		}
		a = ap.Addr()
	}
	return a.Unmap().WithZone(""), true
}

func (s *server) isTrusted(a netip.Addr) bool {
	for _, p := range s.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (s *server) PostAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req oapi.LoginRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	ipKey, emailKey := s.ipKey(r), "email:"+auth.NormalizeEmail(req.Email)
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
		// A server error is not a failed attempt.
		s.limiter.Refund(emailKey)
		s.limiter.Refund(ipKey)
		slog.Error("login", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	// Only failures count: a success clears the email and gives the IP its attempt back.
	s.limiter.Reset(emailKey)
	s.limiter.Refund(ipKey)
	if !s.startSession(w, r, userID) {
		return
	}
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
	me, err := s.sessionMe(r.Context(), sess)
	if err != nil {
		slog.Error("me", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, me)
}

func (s *server) sessionMe(ctx context.Context, sess auth.Session) (oapi.Me, error) {
	roles, tenantName, err := s.store.Membership(ctx, sess)
	if err != nil {
		return oapi.Me{}, err
	}
	me := oapi.Me{UserId: sess.UserID, Email: sess.Email, EmailVerified: sess.EmailVerified,
		Mfa: oapi.MfaState(auth.MFAStateOf(sess, roles...))}
	// A tenant without a role (membership removed after sign-in) is not reported.
	if len(roles) != 0 {
		// Keep the legacy single-role view privileged when a user has several roles.
		r := oapi.Role(roles[len(roles)-1])
		me.Role = &r
		if len(roles) > 1 {
			all := make([]oapi.Role, len(roles))
			for i, role := range roles {
				all[i] = oapi.Role(role)
			}
			me.Roles = &all
		}
		me.TenantId = &sess.TenantID
		me.TenantName = &tenantName
	}
	return me, nil
}

// startSession replaces the request's previous session with a new one for userID and sets its
// cookie; it writes the error response and returns false on failure.
func (s *server) startSession(w http.ResponseWriter, r *http.Request, userID string) bool {
	if err := s.dropPreviousSession(r); err != nil {
		slog.Error("drop previous session", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return false
	}
	token, err := s.store.CreateSession(r.Context(), userID)
	if err != nil {
		slog.Error("create session", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return false
	}
	s.setNewSessionCookie(w, token)
	return true
}
