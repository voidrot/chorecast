package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/voidrot/chorecast/server/internal/api"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/version"
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

func TestUnknownAPIRoutesReturnProblems(t *testing.T) {
	r := chi.NewRouter()
	api.New(r, config.Config{}, nil, clock.Real{})
	for _, tc := range []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodGet, "/api/v1/nope", http.StatusNotFound, "not_found"},
		{http.MethodDelete, "/api/v1/health", http.StatusMethodNotAllowed, "method_not_allowed"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil))
		if rec.Code != tc.status || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s %s = %d %q", tc.method, tc.path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
			t.Errorf("%s %s body = %s", tc.method, tc.path, rec.Body)
		}
	}
}

// The compatibility contract: apiVersion is the major version in the URL prefix.
func TestAPIVersionMatchesPrefix(t *testing.T) {
	if want := fmt.Sprintf("/api/v%d", version.APIVersion); api.Prefix != want {
		t.Errorf("Prefix = %s but APIVersion = %d (want prefix %s)", api.Prefix, version.APIVersion, want)
	}
}

func TestMe(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u, err := store.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Mom", Role: "parent", IsOwner: 1})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.NewStore(store.Queries, clock.Real{}).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenApp, UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	api.New(r, config.Config{}, store, clock.Real{})

	get := func(header string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/me", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := get("Bearer " + token)
	var me struct {
		Kind   string   `json:"kind"`
		UserID int64    `json:"userId"`
		Owner  bool     `json:"owner"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || rec.Code != 200 {
		t.Fatalf("GET /me = %d %s", rec.Code, rec.Body)
	}
	if me.Kind != "parent" || me.UserID != u.ID || !me.Owner || !slices.Contains(me.Scopes, "server:manage") {
		t.Errorf("me = %+v", me)
	}

	if rec := get(""); rec.Code != 401 || !strings.Contains(rec.Body.String(), `"code":"unauthenticated"`) {
		t.Errorf("anonymous GET /me = %d %s", rec.Code, rec.Body)
	}
}
