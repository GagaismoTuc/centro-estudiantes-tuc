package httpapi

import "net/http"

// notImplemented answers the election endpoints whose contract exists but whose handlers come later.
// shortcut: stubs until the create/edit/read election tickets implement them; remove each as it lands.
func notImplemented(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusNotImplemented, "not_implemented")
}

func (s *server) PostElection(w http.ResponseWriter, _ *http.Request) { notImplemented(w) }

func (s *server) GetCurrentElection(w http.ResponseWriter, _ *http.Request) { notImplemented(w) }

func (s *server) GetElection(w http.ResponseWriter, _ *http.Request, _ string) { notImplemented(w) }

func (s *server) PutElection(w http.ResponseWriter, _ *http.Request, _ string) { notImplemented(w) }
