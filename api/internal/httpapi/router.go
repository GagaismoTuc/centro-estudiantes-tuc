// Package httpapi implements the server interface generated from openapi/openapi.yaml.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// server implements oapi.ServerInterface.
type server struct{}

var _ oapi.ServerInterface = server{}

// NewRouter returns the HTTP handler for the whole API, mounted under /api.
func NewRouter() http.Handler {
	return oapi.HandlerWithOptions(server{}, oapi.StdHTTPServerOptions{
		BaseURL:    "/api",
		BaseRouter: http.NewServeMux(),
	})
}

func (server) GetHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, oapi.Health{Status: oapi.Ok})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
