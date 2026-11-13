package roster_test

import (
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

// A nil clock would only fail later, inside a request: NewStore refuses it.
func TestNewStoreRejectsANilClock(t *testing.T) {
	defer func() {
		if got := recover(); got != "roster: NewStore needs a clock" {
			t.Fatalf("panic = %v, want %q", got, "roster: NewStore needs a clock")
		}
	}()
	roster.NewStore(nil, key, nil)
}
