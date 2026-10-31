package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

type fixture struct {
	store *Store
	clock *clock.Fake
	q     *sqlc.Queries
	owner int64
	other int64 // non-owner parent
	kid   int64
	kid2  int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	f := &fixture{q: d.Queries, clock: clock.NewFake(t0)}
	f.store = NewStore(d.Queries, f.clock)
	user := func(name, role string, owner int64) int64 {
		u, err := d.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: name, Role: role, IsOwner: owner})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	f.owner = user("Owner", "parent", 1)
	f.other = user("Parent", "parent", 0)
	f.kid = user("Sam", "child", 0)
	f.kid2 = user("Alex", "child", 0)
	return f
}

func (f *fixture) issue(t *testing.T, kind TokenKind, userID int64, ttl time.Duration) (string, int64) {
	t.Helper()
	tok, id, err := f.store.Issue(t.Context(), IssueParams{Kind: kind, UserID: userID, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	return tok, id
}

var bearerKinds = []TokenKind{TokenApp, TokenDevice, TokenDisplay, TokenAPI}

func TestSecretHashing(t *testing.T) {
	h, err := HashSecret("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected encoding %q", h)
	}
	if ok, rehash, err := VerifySecret("correct horse", h); !ok || rehash || err != nil {
		t.Errorf("verify correct = %v %v %v", ok, rehash, err)
	}
	if ok, _, err := VerifySecret("wrong", h); ok || err != nil {
		t.Errorf("verify wrong = %v %v", ok, err)
	}
	h2, _ := HashSecret("correct horse")
	if h == h2 {
		t.Error("two hashes of the same secret are identical (salt not random)")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=x$a$b", "$argon2id$v=19$m=8,t=1,p=1$!!$aGFzaA"} {
		if _, _, err := VerifySecret("x", bad); err == nil {
			t.Errorf("VerifySecret accepted malformed %q", bad)
		}
	}
	weak, _ := hashSecret("pin", Argon2Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	if ok, rehash, _ := VerifySecret("pin", weak); !ok || !rehash {
		t.Errorf("old-parameter hash should verify and need rehash: ok=%v rehash=%v", ok, rehash)
	}
}

func TestGenerateToken(t *testing.T) {
	a, _ := GenerateToken()
	b, _ := GenerateToken()
	if a == b || len(a) != 43 {
		t.Errorf("tokens %q %q", a, b)
	}
	if string(HashToken(a)) == a || len(HashToken(a)) != 32 {
		t.Error("HashToken should return a 32-byte digest")
	}
}

func TestAuthenticatePrincipals(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	tests := []struct {
		name    string
		kind    TokenKind
		user    int64
		allowed []TokenKind
		want    Kind // "" = rejected
		owner   bool
	}{
		{"owner app token", TokenApp, f.owner, bearerKinds, KindParent, true},
		{"parent app token", TokenApp, f.other, bearerKinds, KindParent, false},
		{"web session via cookie", TokenWebSession, f.other, []TokenKind{TokenWebSession}, KindParent, false},
		{"web session as bearer rejected", TokenWebSession, f.other, bearerKinds, "", false},
		{"app token as cookie rejected", TokenApp, f.other, []TokenKind{TokenWebSession}, "", false},
		{"kid-bound device", TokenDevice, f.kid, bearerKinds, KindKid, false},
		{"shared device", TokenDevice, 0, bearerKinds, KindSharedDevice, false},
		{"device bound to a parent rejected", TokenDevice, f.other, bearerKinds, "", false},
		{"app token for a kid rejected", TokenApp, f.kid, bearerKinds, "", false},
		{"display", TokenDisplay, 0, bearerKinds, KindDisplay, false},
		{"api key", TokenAPI, 0, bearerKinds, KindAPI, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, id := f.issue(t, tt.kind, tt.user, 0)
			p, err := f.store.Authenticate(ctx, tok, tt.allowed...)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if p != nil {
					t.Fatalf("expected rejection, got %+v", p)
				}
				return
			}
			if p == nil || p.Kind != tt.want || p.Owner != tt.owner || p.TokenID != id {
				t.Fatalf("principal = %+v, want kind %s owner %v", p, tt.want, tt.owner)
			}
			if tt.want == KindAPI && len(p.scopes) != 0 {
				t.Errorf("API keys must hold no scopes until per-key grants exist, got %v", p.scopes)
			}
		})
	}

	if p, err := f.store.Authenticate(ctx, "not-a-real-token", bearerKinds...); p != nil || err != nil {
		t.Errorf("unknown token = %v, %v", p, err)
	}
	if p, err := f.store.Authenticate(ctx, "", bearerKinds...); p != nil || err != nil {
		t.Errorf("empty token = %v, %v", p, err)
	}
}

func TestExpiryRevocationAndPurge(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	tok, _ := f.issue(t, TokenApp, f.other, time.Hour)
	if p, _ := f.store.Authenticate(ctx, tok, bearerKinds...); p == nil {
		t.Fatal("fresh token rejected")
	}
	f.clock.Advance(time.Hour)
	if p, _ := f.store.Authenticate(ctx, tok, bearerKinds...); p != nil {
		t.Error("token accepted at its expiry instant")
	}
	if n, err := f.store.PurgeExpired(ctx); err != nil || n != 1 {
		t.Errorf("PurgeExpired = %d, %v", n, err)
	}

	tok2, id2 := f.issue(t, TokenApp, f.other, 0)
	if err := f.store.Revoke(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.store.Authenticate(ctx, tok2, bearerKinds...); p != nil {
		t.Error("revoked token accepted")
	}

	tok3, _ := f.issue(t, TokenApp, f.other, 0)
	tok4, _ := f.issue(t, TokenWebSession, f.other, 0)
	if err := f.store.RevokeAllForUser(ctx, f.other); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.store.Authenticate(ctx, tok3, bearerKinds...); p != nil {
		t.Error("app token survived RevokeAllForUser")
	}
	if p, _ := f.store.Authenticate(ctx, tok4, TokenWebSession); p != nil {
		t.Error("web session survived RevokeAllForUser")
	}
}

func TestLastUsedIsThrottled(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	tok, _ := f.issue(t, TokenApp, f.other, 0)
	lastUsed := func() string {
		row, err := f.q.GetTokenByHash(ctx, HashToken(tok))
		if err != nil {
			t.Fatal(err)
		}
		return row.LastUsedAt.String
	}

	_, _ = f.store.Authenticate(ctx, tok, bearerKinds...)
	first := lastUsed()
	if first != db.Timestamp(t0) {
		t.Fatalf("last_used_at = %q, want %q", first, db.Timestamp(t0))
	}
	f.clock.Advance(30 * time.Second)
	_, _ = f.store.Authenticate(ctx, tok, bearerKinds...)
	if lastUsed() != first {
		t.Error("last_used_at rewritten within the throttle interval")
	}
	f.clock.Advance(time.Minute)
	_, _ = f.store.Authenticate(ctx, tok, bearerKinds...)
	if lastUsed() == first {
		t.Error("last_used_at not updated after the throttle interval")
	}
}

func TestCanAccessKid(t *testing.T) {
	const sam, alex = 10, 11
	tests := []struct {
		p    *Principal
		want map[int64]bool
	}{
		{newPrincipal(KindParent, 1, false, 0), map[int64]bool{sam: true, alex: true}},
		{newPrincipal(KindDisplay, 0, false, 0), map[int64]bool{sam: true, alex: true}},
		{newPrincipal(KindKid, sam, false, 0), map[int64]bool{sam: true, alex: false}},
		{newPrincipal(KindSharedDevice, 0, false, 0), map[int64]bool{sam: false, alex: false}},
		{newPrincipal(KindAPI, 0, false, 0), map[int64]bool{sam: false, alex: false}},
	}
	for _, tt := range tests {
		for kid, want := range tt.want {
			if got := tt.p.CanAccessKid(kid); got != want {
				t.Errorf("%s(user %d).CanAccessKid(%d) = %v, want %v", tt.p.Kind, tt.p.UserID, kid, got, want)
			}
		}
	}
}

// TestMiddleware exercises each principal kind against operations with different policies.
func TestMiddleware(t *testing.T) {
	f := newFixture(t)
	_, api := humatest.New(t)
	api.UseMiddleware(Middleware(api, f.store))

	type out struct {
		Body struct {
			Kind string `json:"kind"`
		}
	}
	handler := func(ctx context.Context, _ *struct{}) (*out, error) {
		o := &out{}
		if p, ok := PrincipalFrom(ctx); ok {
			o.Body.Kind = string(p.Kind)
		}
		return o, nil
	}
	reg := func(id, path string, security []map[string][]string, meta map[string]any) {
		huma.Register(api, huma.Operation{OperationID: id, Method: http.MethodGet, Path: path, Security: security, Metadata: meta}, handler)
	}
	reg("public", "/public", nil, Public())
	reg("household", "/household", Require(ScopeHouseholdManage), nil)
	reg("server", "/server", Require(ScopeServerManage), nil)
	reg("complete", "/complete", Require(ScopeChoresComplete), nil)
	reg("profiles", "/profiles", Require(ScopeProfilesSelect), nil)
	reg("read", "/read", Require(ScopeHouseholdRead), nil)
	reg("undeclared", "/undeclared", nil, nil)

	owner, _ := f.issue(t, TokenApp, f.owner, 0)
	parent, _ := f.issue(t, TokenApp, f.other, 0)
	session, _ := f.issue(t, TokenWebSession, f.other, 0)
	kid, _ := f.issue(t, TokenDevice, f.kid, 0)
	shared, _ := f.issue(t, TokenDevice, 0, 0)
	display, _ := f.issue(t, TokenDisplay, 0, 0)
	apiKey, _ := f.issue(t, TokenAPI, 0, 0)

	bearer := func(tok string) string { return "Authorization: Bearer " + tok }
	cookie := func(tok string) string { return "Cookie: " + SessionCookie + "=" + tok }

	type call struct {
		cred string
		want map[string]int // path -> status
	}
	all := func(status int) map[string]int {
		return map[string]int{"/household": status, "/server": status, "/complete": status, "/profiles": status, "/read": status}
	}
	calls := map[string]call{
		"anonymous":         {"", all(401)},
		"bad header":        {"Authorization: Basic abc", all(401)},
		"owner":             {bearer(owner), all(200)},
		"parent":            {bearer(parent), map[string]int{"/household": 200, "/server": 403, "/complete": 200, "/profiles": 200, "/read": 200}},
		"session":           {cookie(session), map[string]int{"/household": 200, "/server": 403, "/complete": 200, "/profiles": 200, "/read": 200}},
		"session as bearer": {bearer(session), all(401)},
		"kid":               {bearer(kid), map[string]int{"/household": 403, "/server": 403, "/complete": 200, "/profiles": 403, "/read": 200}},
		"shared":            {bearer(shared), map[string]int{"/household": 403, "/server": 403, "/complete": 403, "/profiles": 200, "/read": 200}},
		"display":           {bearer(display), map[string]int{"/household": 403, "/server": 403, "/complete": 403, "/profiles": 403, "/read": 200}},
		"api key":           {bearer(apiKey), all(403)},
	}
	for name, c := range calls {
		t.Run(name, func(t *testing.T) {
			var args []any
			if c.cred != "" {
				args = append(args, c.cred)
			}
			for path, want := range c.want {
				if resp := api.Get(path, args...); resp.Code != want {
					t.Errorf("GET %s = %d, want %d (%s)", path, resp.Code, want, resp.Body.String())
				}
			}
			// Public operations always succeed, and see the principal when one is presented.
			if resp := api.Get("/public", args...); resp.Code != 200 {
				t.Errorf("GET /public = %d", resp.Code)
			}
			// Operations without a declared policy are refused even to owners.
			if resp := api.Get("/undeclared", args...); resp.Code != 403 {
				t.Errorf("GET /undeclared = %d, want 403", resp.Code)
			}
		})
	}

	resp := api.Get("/public", bearer(kid))
	if !strings.Contains(resp.Body.String(), `"kind":"kid"`) {
		t.Errorf("public handler did not see the principal: %s", resp.Body.String())
	}
}

func TestCheckOperations(t *testing.T) {
	_, api := humatest.New(t)
	noop := func(context.Context, *struct{}) (*struct{}, error) { return nil, nil }
	huma.Register(api, huma.Operation{OperationID: "ok", Method: http.MethodGet, Path: "/ok", Security: Require(ScopeChoresRead)}, noop)
	huma.Register(api, huma.Operation{OperationID: "pub", Method: http.MethodGet, Path: "/pub", Metadata: Public()}, noop)
	if err := CheckOperations(api.OpenAPI()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	huma.Register(api, huma.Operation{OperationID: "forgot", Method: http.MethodPost, Path: "/forgot"}, noop)
	err := CheckOperations(api.OpenAPI())
	if err == nil || !strings.Contains(err.Error(), "POST /forgot (forgot)") {
		t.Fatalf("CheckOperations = %v, want it to name POST /forgot", err)
	}
}

func TestSchemaRejectsInvalidRows(t *testing.T) {
	f := newFixture(t)
	if _, err := f.q.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Kid", Role: "child", IsOwner: 1}); err == nil {
		t.Error("a kid was allowed to be an owner")
	}
	for _, k := range []TokenKind{TokenWebSession, TokenApp} {
		if _, _, err := f.store.Issue(t.Context(), IssueParams{Kind: k}); err == nil {
			t.Errorf("%s token without a user was allowed", k)
		}
	}
}
