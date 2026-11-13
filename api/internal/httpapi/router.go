// Package httpapi implements the server interface generated from openapi/openapi.yaml.
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// Deps are the router's dependencies. Pool must connect as a member of ce_app.
type Deps struct {
	Pool *pgxpool.Pool
	Now  func() time.Time // defaults to time.Now
}

// server implements oapi.ServerInterface.
type server struct {
	store   *auth.Store
	limiter *auth.Limiter
}

var _ oapi.ServerInterface = (*server)(nil)

// Sign-in limit: failed attempts per client IP and per email.
const (
	loginMaxFailures = 5
	loginWindow      = 15 * time.Minute
)

// NewRouter returns the HTTP handler for the whole API, mounted under /api. Cross-origin
// state-changing requests are rejected before routing, and the session cookie, when present,
// is resolved into the request context.
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &server{
		store:   auth.NewStore(d.Pool, d.Now),
		limiter: auth.NewLimiter(loginMaxFailures, loginWindow, d.Now),
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
