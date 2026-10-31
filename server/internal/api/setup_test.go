package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/voidrot/chorecast/server/internal/api"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/setup"
)

type setupServer struct {
	t     *testing.T
	store *db.DB
	svc   *setup.Service
	clk   *clock.Fake
	r     chi.Router
}

func newSetupServer(t *testing.T) *setupServer {
	t.Helper()
	return newSetupServerWith(t, config.Config{})
}

func newSetupServerWith(t *testing.T, cfg config.Config) *setupServer {
	t.Helper()
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clk := clock.NewFake(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	svc, err := setup.New(t.Context(), store, clk)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	api.New(r, api.Deps{Config: cfg, DB: store, Clock: clk, Setup: svc})
	return &setupServer{t: t, store: store, svc: svc, clk: clk, r: r}
}

func (s *setupServer) do(method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	s.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			s.t.Fatal(err)
		}
	}
	req := httptest.NewRequestWithContext(s.t.Context(), method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	return rec
}

func wantCode(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || (code != "" && !strings.Contains(rec.Body.String(), `"code":"`+code+`"`)) {
		t.Fatalf("%s = %d %s, want %d %s", what, rec.Code, rec.Body, status, code)
	}
}

func setupRequest(token string) map[string]any {
	return map[string]any{
		"token":     token,
		"household": map[string]any{"name": "The Bradys", "timezone": "America/Chicago", "dayEnd": "21:00", "weekStart": "sunday"},
		"owner":     map[string]any{"displayName": "Mom", "username": "mom", "password": "correct horse battery"},
		"kids": []map[string]any{
			{"displayName": "Sam", "birthDate": "2018-04-02", "color": "#3B82F6", "pinRequired": true},
			{"displayName": "Sam", "color": "#22c55e"},
		},
	}
}

// Fresh server → wizard → owner signed in; a second setup is refused.
func TestSetupFlow(t *testing.T) {
	s := newSetupServer(t)

	wantCode(t, "GET /me before setup", s.do("GET", "/api/v1/me", nil), 503, "setup_required")
	wantCode(t, "GET /health before setup", s.do("GET", "/api/v1/health", nil), 200, "")
	if rec := s.do("GET", "/api/v1/setup", nil); !strings.Contains(rec.Body.String(), `"required":true`) {
		t.Fatalf("GET /setup = %s", rec.Body)
	}

	// The token is replaced on every start: the first one stops working.
	stale, err := s.svc.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.svc.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "verify stale token", s.do("POST", "/api/v1/setup/verify", map[string]string{"token": stale}), 403, "invalid_setup_token")
	wantCode(t, "verify token", s.do("POST", "/api/v1/setup/verify", map[string]string{"token": token}), 204, "")
	wantCode(t, "setup with stale token", s.do("POST", "/api/v1/setup", setupRequest(stale)), 403, "invalid_setup_token")

	bad := setupRequest(token)
	bad["household"].(map[string]any)["timezone"] = "Mars/Olympus"
	bad["kids"].([]map[string]any)[0]["birthDate"] = "2030-01-01"
	rec := s.do("POST", "/api/v1/setup", bad)
	wantCode(t, "setup with invalid fields", rec, 422, "validation_failed")
	for _, loc := range []string{"body.household.timezone", "body.kids[0].birthDate"} {
		if !strings.Contains(rec.Body.String(), loc) {
			t.Errorf("422 body %s missing %s", rec.Body, loc)
		}
	}

	rec = s.do("POST", "/api/v1/setup", setupRequest(token))
	wantCode(t, "complete setup", rec, 201, "")
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			session = c
		}
	}
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", session)
	}

	// The owner is signed in.
	rec = s.do("GET", "/api/v1/me", nil, session)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kind":"parent"`) || !strings.Contains(rec.Body.String(), `"owner":true`) {
		t.Fatalf("GET /me after setup = %d %s", rec.Code, rec.Body)
	}
	wantCode(t, "anonymous GET /me after setup", s.do("GET", "/api/v1/me", nil), 401, "unauthenticated")

	// Everything was created.
	hh, err := s.store.GetHousehold(t.Context())
	if err != nil || hh.Name != "The Bradys" || hh.Timezone != "America/Chicago" || hh.DayEnd != "21:00" || hh.WeekStart != 0 {
		t.Errorf("household = %+v, %v", hh, err)
	}
	users, err := s.store.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, u := range users {
		switch u.Role {
		case "parent":
			ok, _, err := auth.VerifySecret("correct horse battery", u.PasswordHash.String)
			if !ok || err != nil || u.IsOwner != 1 || u.Username.String != "mom" {
				t.Errorf("owner = %+v (password ok %v, %v)", u, ok, err)
			}
		case "child":
			slugs = append(slugs, u.Slug.String)
			if u.Slug.String == "sam" && (u.Color.String != "#3b82f6" || u.BirthDate.String != "2018-04-02" || u.PinRequired != 1) {
				t.Errorf("kid = %+v", u)
			}
		}
	}
	if strings.Join(slugs, ",") != "sam,sam-2" && strings.Join(slugs, ",") != "sam-2,sam" {
		t.Errorf("kid slugs = %v", slugs)
	}

	// A second run is refused, even with the old token.
	wantCode(t, "second setup", s.do("POST", "/api/v1/setup", setupRequest(token)), 409, "setup_complete")
	wantCode(t, "verify after setup", s.do("POST", "/api/v1/setup/verify", map[string]string{"token": token}), 409, "setup_complete")
	if rec := s.do("GET", "/api/v1/setup", nil); !strings.Contains(rec.Body.String(), `"required":false`) {
		t.Errorf("GET /setup after setup = %s", rec.Body)
	}
	if tok, err := s.svc.Begin(t.Context()); tok != "" || err != nil {
		t.Errorf("Begin after setup = %q, %v", tok, err)
	}

	// A restart reads the completed state from the database.
	again, err := setup.New(t.Context(), s.store, clock.Real{})
	if err != nil || again.Required() {
		t.Errorf("setup.New after setup: required=%v err=%v", again.Required(), err)
	}
}

func TestSetupSessionCookieSecureBehindHTTPS(t *testing.T) {
	s := newSetupServer(t)
	token, err := s.svc.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(setupRequest(token))
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/v1/setup", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	if rec.Code != 201 || len(rec.Result().Cookies()) != 1 || !rec.Result().Cookies()[0].Secure {
		t.Fatalf("setup over https = %d, cookies %+v", rec.Code, rec.Result().Cookies())
	}
}
