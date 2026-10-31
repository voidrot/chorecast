package idempotency

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

type harness struct {
	api   humatest.TestAPI
	db    *db.DB
	q     *sqlc.Queries
	clock *clock.Fake
	calls atomic.Int64
	fail  atomic.Bool // make the handler return 500
}

// newHarness builds an API whose callers are identified by an X-Test-Token header (standing in
// for auth.Middleware), with POST /things counting calls and GET /things for contrast.
func newHarness(t *testing.T) *harness {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	h := &harness{db: d, q: d.Queries, clock: clock.NewFake(t0)}

	cfg := huma.DefaultConfig("test", "1")
	cfg.OnAddOperation = append(cfg.OnAddOperation, AddHeaderParam)
	_, api := humatest.New(t, cfg)
	h.api = api
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if tok := ctx.Header("X-Test-Token"); tok != "" {
			id := int64(len(tok)) // distinct tokens -> distinct ids in these tests
			p := &auth.Principal{Kind: auth.KindParent, UserID: 1, TokenID: id}
			ctx = huma.WithContext(ctx, auth.WithPrincipal(ctx.Context(), p))
		}
		next(ctx)
	}, Middleware(api, h.q, h.clock))

	type in struct {
		Body struct {
			Name string `json:"name"`
		}
	}
	type out struct {
		Body struct {
			N    int64  `json:"n"`
			Name string `json:"name"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create", Method: http.MethodPost, Path: "/things", DefaultStatus: 201},
		func(_ context.Context, i *in) (*out, error) {
			n := h.calls.Add(1)
			if h.fail.Load() {
				return nil, huma.Error500InternalServerError("database exploded")
			}
			o := &out{}
			o.Body.N, o.Body.Name = n, i.Body.Name
			return o, nil
		})
	huma.Register(api, huma.Operation{OperationID: "list", Method: http.MethodGet, Path: "/things"},
		func(context.Context, *struct{}) (*struct{}, error) { h.calls.Add(1); return nil, nil })
	return h
}

const callerA, callerB = "X-Test-Token: a", "X-Test-Token: bb"

func key(k string) string { return Header + ": " + k }

func (h *harness) post(t *testing.T, body any, headers ...any) (int, string, http.Header) {
	t.Helper()
	resp := h.api.Post("/things", append(headers, body)...)
	return resp.Code, resp.Body.String(), resp.Header()
}

func code(t *testing.T, body string) string {
	t.Helper()
	var p apierr.Problem
	_ = json.Unmarshal([]byte(body), &p)
	return p.Code
}

func TestReplaySameRequest(t *testing.T) {
	h := newHarness(t)
	s1, b1, hdr1 := h.post(t, map[string]any{"name": "x"}, callerA, key("k1"))
	s2, b2, hdr2 := h.post(t, map[string]any{"name": "x"}, callerA, key("k1"))
	if s1 != 201 || s2 != 201 || b1 != b2 {
		t.Fatalf("first %d %s, replay %d %s", s1, b1, s2, b2)
	}
	if h.calls.Load() != 1 {
		t.Errorf("handler ran %d times, want 1", h.calls.Load())
	}
	if hdr1.Get(ReplayedHeader) != "" || hdr2.Get(ReplayedHeader) != "true" {
		t.Errorf("replay header: first %q, second %q", hdr1.Get(ReplayedHeader), hdr2.Get(ReplayedHeader))
	}
	if hdr2.Get("Content-Type") != hdr1.Get("Content-Type") {
		t.Errorf("content type changed on replay: %q vs %q", hdr1.Get("Content-Type"), hdr2.Get("Content-Type"))
	}
}

func TestWithoutKeyOrNotApplicable(t *testing.T) {
	h := newHarness(t)
	h.post(t, map[string]any{"name": "x"}, callerA)
	h.post(t, map[string]any{"name": "x"}, callerA)
	if h.calls.Load() != 2 {
		t.Errorf("no key: handler ran %d times, want 2", h.calls.Load())
	}
	h.calls.Store(0)
	h.api.Get("/things", callerA, key("g"))
	h.api.Get("/things", callerA, key("g"))
	if h.calls.Load() != 2 {
		t.Errorf("GET: handler ran %d times, want 2", h.calls.Load())
	}
	h.calls.Store(0)
	h.post(t, map[string]any{"name": "x"}, key("anon")) // unauthenticated: not applied
	h.post(t, map[string]any{"name": "x"}, key("anon"))
	if h.calls.Load() != 2 {
		t.Errorf("anonymous: handler ran %d times, want 2", h.calls.Load())
	}
}

func TestKeyReuseWithDifferentBody(t *testing.T) {
	h := newHarness(t)
	h.post(t, map[string]any{"name": "x"}, callerA, key("k"))
	s, b, _ := h.post(t, map[string]any{"name": "y"}, callerA, key("k"))
	if s != 422 || code(t, b) != apierr.CodeIdempotencyReuse {
		t.Errorf("got %d %s", s, b)
	}
}

func TestKeysAreScopedToToken(t *testing.T) {
	h := newHarness(t)
	h.post(t, map[string]any{"name": "x"}, callerA, key("k"))
	h.post(t, map[string]any{"name": "x"}, callerB, key("k"))
	if h.calls.Load() != 2 {
		t.Errorf("handler ran %d times, want 2 (one per token)", h.calls.Load())
	}
}

func TestServerErrorsAreNotStored(t *testing.T) {
	h := newHarness(t)
	h.fail.Store(true)
	if s, _, _ := h.post(t, map[string]any{"name": "x"}, callerA, key("k")); s != 500 {
		t.Fatalf("got %d", s)
	}
	h.fail.Store(false)
	if s, _, hdr := h.post(t, map[string]any{"name": "x"}, callerA, key("k")); s != 201 || hdr.Get(ReplayedHeader) != "" {
		t.Errorf("retry after 500: %d replayed=%q", s, hdr.Get(ReplayedHeader))
	}
	if h.calls.Load() != 2 {
		t.Errorf("handler ran %d times, want 2", h.calls.Load())
	}
}

func TestInProgressAbandonedAndExpired(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{"name": "x"}
	ctx := t.Context()

	h.post(t, body, callerA, key("k")) // calls=1, stored at t0
	// Simulate the first request still running (or crashed mid-request): no stored response.
	if _, err := h.db.SQL.ExecContext(ctx, `UPDATE idempotency_keys SET status = NULL, body = NULL`); err != nil {
		t.Fatal(err)
	}
	if s, b, _ := h.post(t, body, callerA, key("k")); s != 409 || code(t, b) != apierr.CodeIdempotencyInFlight {
		t.Fatalf("in progress: %d %s", s, b)
	}
	h.clock.Advance(abandonAfter)
	if s, _, _ := h.post(t, body, callerA, key("k")); s != 201 || h.calls.Load() != 2 {
		t.Fatalf("abandoned key not reclaimed: %d calls=%d", s, h.calls.Load())
	}

	// The reclaimed key replays until its TTL, then runs again.
	h.clock.Advance(TTL - time.Second)
	if _, _, hdr := h.post(t, body, callerA, key("k")); hdr.Get(ReplayedHeader) != "true" || h.calls.Load() != 2 {
		t.Fatalf("before TTL: replayed=%q calls=%d", hdr.Get(ReplayedHeader), h.calls.Load())
	}
	h.clock.Advance(time.Second)
	if _, _, hdr := h.post(t, body, callerA, key("k")); hdr.Get(ReplayedHeader) != "" || h.calls.Load() != 3 {
		t.Fatalf("at TTL: replayed=%q calls=%d", hdr.Get(ReplayedHeader), h.calls.Load())
	}

	if n, err := Purge(ctx, h.q, h.clock.Now().Add(TTL+time.Second)); err != nil || n != 1 {
		t.Errorf("Purge = %d, %v", n, err)
	}
}

func TestInvalidKeysAndNonJSON(t *testing.T) {
	h := newHarness(t)
	for _, k := range []string{strings.Repeat("a", 256), "has space", "tab\tkey"} {
		if s, b, _ := h.post(t, map[string]any{"name": "x"}, callerA, key(k)); s != 400 || code(t, b) != apierr.CodeInvalidIdempotency {
			t.Errorf("key %q: %d %s", k, s, b)
		}
	}
	if h.calls.Load() != 0 {
		t.Errorf("handler ran for invalid keys")
	}
	if !isJSON("application/json; charset=utf-8") || !isJSON("application/merge-patch+json") || isJSON("multipart/form-data") {
		t.Error("isJSON")
	}
}

func TestHeaderDocumentedOnPostOnly(t *testing.T) {
	h := newHarness(t)
	oapi := h.api.OpenAPI()
	has := func(op *huma.Operation) bool {
		for _, p := range op.Parameters {
			if p.Name == Header && p.In == "header" {
				return true
			}
		}
		return false
	}
	if !has(oapi.Paths["/things"].Post) || has(oapi.Paths["/things"].Get) {
		t.Error("Idempotency-Key should be documented on POST only")
	}
}
