package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voidrot/chorecast/server/internal/horizon"
)

// keyedPost sends a JSON POST carrying an Idempotency-Key through the real router.
func (s *setupServer) keyedPost(path, key, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequestWithContext(s.t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	return rec
}

// A keyed POST answers normally and stores its response; a retry with the same key gets that
// response back without the action running again.
func TestKeyedPostIsStoredAndReplayed(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	balance := func() int64 {
		t.Helper()
		rec := s.do("GET", "/api/v1/kids/sam/points", nil, mom)
		wantCode(t, "balance", rec, 200, "")
		return decode[struct{ Balance int64 }](t, rec).Balance
	}

	const adjust = "/api/v1/kids/sam/points/adjustments"
	first := s.keyedPost(adjust, "adj-1", `{"amount":20,"note":"good week"}`, mom)
	wantCode(t, "keyed adjustment", first, 201, "")
	if first.Header().Get("Idempotent-Replayed") != "" {
		t.Error("first response marked as a replay")
	}
	replay := s.keyedPost(adjust, "adj-1", `{"amount":20,"note":"good week"}`, mom)
	wantCode(t, "replayed adjustment", replay, 201, "")
	if replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != first.Body.String() {
		t.Errorf("replay = %v %s, want the stored response %s", replay.Header(), replay.Body, first.Body)
	}
	if got := balance(); got != 20 {
		t.Errorf("balance %d after a replay, want 20 (the action ran once)", got)
	}
	wantCode(t, "same key, other body", s.keyedPost(adjust, "adj-1", `{"amount":5,"note":"other"}`, mom), 422, "idempotency_key_reuse")
	wantCode(t, "new key", s.keyedPost(adjust, "adj-2", `{"amount":5,"note":"again"}`, mom), 201, "")
	if got := balance(); got != 25 {
		t.Errorf("balance %d, want 25", got)
	}
}

// An error response is stored and replayed too, and the key is not left in flight.
func TestKeyedPostStoresClientErrors(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	const path = "/api/v1/kids/nobody/points/adjustments"
	wantCode(t, "unknown kid", s.keyedPost(path, "k", `{"amount":5,"note":"x"}`, mom), 404, "kid_not_found")
	rec := s.keyedPost(path, "k", `{"amount":5,"note":"x"}`, mom)
	wantCode(t, "replayed error", rec, 404, "kid_not_found")
	if rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Error("error response not replayed from the store")
	}
}

// A keyed approval commits once: the retry replays the 200 instead of failing with
// instance_not_submitted, and the kid is paid once.
func TestKeyedApprovalIsReplayed(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	sam, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	wantCode(t, "create", s.do("POST", "/api/v1/chores", map[string]any{
		"name": "Dishes", "points": 4, "requiresApproval": true, "evidence": map[string]any{"photos": 0},
		"schedule":   map[string]any{"type": "daily", "startDate": "2026-10-31"},
		"assignment": map[string]any{"mode": "fixed", "kidIds": []int64{sam.ID}},
	}, mom), 201, "")
	if _, err := horizon.New(s.store, s.clk).Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, i := range decode[struct{ Items []transitionJSON }](t, s.do("GET", "/api/v1/instances", nil, mom)).Items {
		if i.Date == "2026-10-31" {
			id = i.ID
		}
	}
	if id == 0 {
		t.Fatal("no instance for today")
	}
	wantCode(t, "complete", s.keyedPost(fmt.Sprintf("/api/v1/instances/%d/completion", id), "c-1", `{}`, mom), 200, "")

	approve := fmt.Sprintf("/api/v1/instances/%d/approval", id)
	first := s.keyedPost(approve, "a-1", `{}`, mom)
	wantCode(t, "approve", first, 200, "")
	replay := s.keyedPost(approve, "a-1", `{}`, mom)
	wantCode(t, "replayed approval", replay, 200, "")
	if replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != first.Body.String() {
		t.Errorf("replay = %v %s, want the stored response %s", replay.Header(), replay.Body, first.Body)
	}
	if got := decode[struct{ Balance int64 }](t, s.do("GET", "/api/v1/kids/sam/points", nil, mom)).Balance; got != 4 {
		t.Errorf("balance %d, want 4", got)
	}
}
