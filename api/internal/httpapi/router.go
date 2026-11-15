// Package httpapi implements the server interface generated from openapi/openapi.yaml.
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
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
	// Background runs mail delivery after the response is decided, so response time does not reveal
	// whether an account exists. cmd/server passes a bounded mail.Queue it drains on shutdown; the
	// default is a queue of its own (4 workers, 256 messages). Tests pass a synchronous runner.
	Background func(func())
	// TrustedProxies are the reverse proxies (TRUSTED_PROXIES) whose X-Forwarded-For is believed when
	// they are the TCP peer. Empty: the peer address is always the client.
	TrustedProxies []netip.Prefix
}

// server implements oapi.ServerInterface.
type server struct {
	now     func() time.Time
	trusted []netip.Prefix
	store   *auth.Store
	limiter *auth.Limiter
	// claimLimiter counts invalid access codes per client IP (claim and redeem).
	claimLimiter *auth.Limiter
	roster       *roster.Store
	finance      *finance.Store
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

// Limits per 15-minute window. Per-IP limits are sized for a whole school behind one NAT address;
// per-email and per-user limits stay tight.
const (
	perIPMax         = 50
	loginMaxFailures = 5 // per email
	loginWindow      = 15 * time.Minute
	claimWindow      = 15 * time.Minute
	mailMaxRequests  = 5 // per email
	mailWindow       = 15 * time.Minute
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
		d.Background = mail.NewQueue(4, 256).Go
	}
	s := &server{
		now:     d.Now,
		trusted: d.TrustedProxies,
		store:   auth.NewStore(d.Pool, d.Now),
		limiter: auth.NewLimiter(loginMaxFailures, loginWindow, d.Now).WithMax("ip:", perIPMax),
		roster:  roster.NewStore(d.Pool, d.AccessCodeKey, d.Now),
		finance: finance.NewStore(d.Pool, d.Now),

		claimLimiter: auth.NewLimiter(perIPMax, claimWindow, d.Now),
		mailLimiter:  auth.NewLimiter(mailMaxRequests, mailWindow, d.Now).WithMax("ip:", perIPMax),
		tokenLimiter: auth.NewLimiter(perIPMax, tokenWindow, d.Now),
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
