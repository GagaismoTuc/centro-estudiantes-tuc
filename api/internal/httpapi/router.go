// Package httpapi implements the server interface generated from openapi/openapi.yaml.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

// Deps are the router's dependencies. Pool must connect as a member of ce_app.
type Deps struct {
	Pool *pgxpool.Pool
	Now  func() time.Time // defaults to time.Now
	// AccessCodeKey is the HMAC key for access codes (ACCESS_CODE_KEY, at least 32 bytes).
	AccessCodeKey []byte
	// TOTPKey encrypts TOTP secrets (TOTP_ENC_KEY, exactly 32 bytes).
	TOTPKey []byte
	// Mail delivers verification, sign-in and reset links; nil makes every send fail (logged).
	Mail mail.Sender
	// BaseURL is the public origin of the web app (APP_BASE_URL) used in emailed links, without a
	// trailing slash.
	BaseURL string
	// Background runs mail delivery after the response is decided; defaults to a new goroutine, so
	// response time does not reveal whether an account exists. Tests pass a synchronous runner.
	Background func(func())
}

// server implements oapi.ServerInterface.
type server struct {
	store   *auth.Store
	limiter *auth.Limiter
	// claimLimiter counts invalid access codes per client IP (claim and redeem).
	claimLimiter *auth.Limiter
	roster       *roster.Store
	// mailLimiter counts emailed-link requests per client IP and per email.
	mailLimiter *auth.Limiter
	// tokenLimiter counts invalid emailed tokens per client IP.
	tokenLimiter *auth.Limiter
	mfa          *auth.MFA
	// mfaLimiter counts invalid two-factor codes per user (verify and enrollment confirmation).
	mfaLimiter *auth.Limiter
	mail       mail.Sender
	baseURL    string
	background func(func())
}

var _ oapi.ServerInterface = (*server)(nil)

// Sign-in limit: failed attempts per client IP and per email.
const (
	loginMaxFailures = 5
	loginWindow      = 15 * time.Minute
	claimMaxFailures = 10
	claimWindow      = 15 * time.Minute
	mailMaxRequests  = 5
	mailWindow       = 15 * time.Minute
	tokenMaxFailures = 10
	tokenWindow      = 15 * time.Minute
	mfaMaxFailures   = 5
	mfaWindow        = 15 * time.Minute
)

// NewRouter returns the HTTP handler for the whole API, mounted under /api. Cross-origin
// state-changing requests are rejected before routing, and the session cookie, when present,
// is resolved into the request context.
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Mail == nil {
		d.Mail = noSender{}
	}
	if d.Background == nil {
		d.Background = func(fn func()) { go fn() }
	}
	s := &server{
		store:   auth.NewStore(d.Pool, d.Now),
		limiter: auth.NewLimiter(loginMaxFailures, loginWindow, d.Now),
		roster:  roster.NewStore(d.Pool, d.AccessCodeKey, d.Now),

		claimLimiter: auth.NewLimiter(claimMaxFailures, claimWindow, d.Now),
		mailLimiter:  auth.NewLimiter(mailMaxRequests, mailWindow, d.Now),
		tokenLimiter: auth.NewLimiter(tokenMaxFailures, tokenWindow, d.Now),
		mfa:          auth.NewMFA(d.Pool, d.TOTPKey, d.Now),
		mfaLimiter:   auth.NewLimiter(mfaMaxFailures, mfaWindow, d.Now),
		mail:         d.Mail,
		baseURL:      strings.TrimSuffix(d.BaseURL, "/"),
		background:   d.Background,
	}
	h := oapi.HandlerWithOptions(s, oapi.StdHTTPServerOptions{
		BaseURL:    "/api",
		BaseRouter: http.NewServeMux(),
	})
	return http.NewCrossOriginProtection().Handler(s.withSession(h))
}

func (*server) GetHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, oapi.Health{Status: oapi.Ok})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, oapi.Error{Error: code})
}
