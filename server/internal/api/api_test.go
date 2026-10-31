package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/voidrot/chorecast/server/internal/api"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
)

func TestSystemEndpoints(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	r := chi.NewRouter()
	api.New(r, config.Config{}, store, clock.Real{})

	for path, want := range map[string]string{
		"/api/v1/health":  `"status":"ok"`,
		"/api/v1/version": `"apiVersion":1`,
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: body %s missing %s", path, rec.Body, want)
		}
	}
}

// Every operation must declare required scopes or be explicitly public. api.New also panics
// on violations; this test makes the failure visible in CI with the operation names.
func TestEveryOperationDeclaresAccessPolicy(t *testing.T) {
	hapi := api.New(chi.NewRouter(), config.Config{}, nil, clock.Real{})
	if err := auth.CheckOperations(hapi.OpenAPI()); err != nil {
		t.Fatal(err)
	}
}
