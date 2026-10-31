package api_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/sso"
)

const ssoClientID = "chorecast"

// fakeIdP is a minimal OIDC provider: discovery, JWKS, and a token endpoint that checks PKCE and
// signs ID tokens. Tests "sign in" a subject with authorize() instead of driving a login page.
type fakeIdP struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	clk    clock.Clock
	mu     sync.Mutex
	grants map[string]grant // code -> grant
}

type grant struct{ subject, nonce, challenge, redirectURI string }

func newFakeIdP(t *testing.T, clk clock.Clock) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{t: t, key: key, clk: clk, grants: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": idp.srv.URL, "authorization_endpoint": idp.srv.URL + "/authorize",
			"token_endpoint": idp.srv.URL + "/token", "jwks_uri": idp.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", idp.token)
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

// authorize signs subject in for the authorization URL the server redirected to and returns the
// callback URL the IdP would send the browser to.
func (idp *fakeIdP) authorize(authURL, subject string) string {
	idp.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		idp.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != ssoClientID || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
		idp.t.Fatalf("bad authorization request: %s", authURL)
	}
	code, _ := auth.GenerateToken()
	idp.mu.Lock()
	idp.grants[code] = grant{subject: subject, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
	idp.mu.Unlock()
	cb, _ := url.Parse(q.Get("redirect_uri"))
	cb.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	return cb.String()
}

func (idp *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	idp.mu.Lock()
	g, ok := idp.grants[r.Form.Get("code")]
	delete(idp.grants, r.Form.Get("code"))
	idp.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || r.Form.Get("redirect_uri") != g.redirectURI {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: idp.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	now := idp.clk.Now()
	raw, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer: idp.srv.URL, Subject: g.subject, Audience: jwt.Audience{ssoClientID},
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(5 * time.Minute)),
	}).Claims(map[string]any{"nonce": g.nonce}).Serialize()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw, "expires_in": 300})
}

func newSSOServer(t *testing.T) (*setupServer, *fakeIdP) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	idp := newFakeIdP(t, clk)
	s := newAuthServerWith(t, config.Config{
		PublicURL: "http://chores.test",
		OIDC:      config.OIDCConfig{Issuer: idp.srv.URL, ClientID: ssoClientID, ClientSecret: "secret"},
	})
	s.clk.Set(clk.Now())
	idp.clk = s.clk
	return s, idp
}

// navigate follows a redirect response: returns its Location and the cookies it set.
func redirectOf(t *testing.T, what string, rec *httptest.ResponseRecorder) (string, []*http.Cookie) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("%s = %d %s, want 302", what, rec.Code, rec.Body)
	}
	return rec.Header().Get("Location"), rec.Result().Cookies()
}

func cookieNamed(cs []*http.Cookie, name string) *http.Cookie {
	for _, c := range cs {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

// signIn runs start → IdP → callback and returns the final redirect and cookies.
func signIn(t *testing.T, s *setupServer, idp *fakeIdP, startPath, subject string, cookies ...*http.Cookie) (string, []*http.Cookie) {
	t.Helper()
	authURL, set := redirectOf(t, "start", s.do("GET", startPath, nil, cookies...))
	state := cookieNamed(set, sso.StateCookie)
	if state == nil || !state.HttpOnly {
		t.Fatalf("state cookie = %+v", state)
	}
	cb := idp.authorize(authURL, subject)
	if !strings.HasPrefix(cb, "http://chores.test"+sso.CallbackPath) {
		t.Fatalf("redirect_uri = %s", cb)
	}
	path := strings.TrimPrefix(cb, "http://chores.test")
	return redirectOf(t, "callback", s.do("GET", path, nil, state))
}

func TestSSOLinkAndWebLogin(t *testing.T) {
	s, idp := newSSOServer(t)
	web := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	if !strings.Contains(s.do("GET", "/api/v1/version", nil).Body.String(), `"oidc":true`) {
		t.Error("/version doesn't report oidc")
	}

	// Nobody is linked yet: SSO sign-in is refused, not provisioned.
	loc, _ := signIn(t, s, idp, "/api/v1/auth/oidc/start?client=web&redirect=/account", "alice")
	if loc != "/login?redirect=%2Faccount&sso_error=not_linked" {
		t.Errorf("unlinked sign-in → %s", loc)
	}

	// Link while signed in.
	loc, _ = signIn(t, s, idp, "/api/v1/account/oidc/link", "alice", web)
	if loc != "/account?sso=linked" {
		t.Fatalf("link → %s", loc)
	}
	if !strings.Contains(s.do("GET", "/api/v1/me", nil, web).Body.String(), `"oidcLinked":true`) {
		t.Error("/me doesn't report oidcLinked")
	}

	// Now SSO signs in, returning to the requested page with a session.
	loc, set := signIn(t, s, idp, "/api/v1/auth/oidc/start?client=web&redirect=/account", "alice")
	if loc != "/account" {
		t.Errorf("sign-in → %s", loc)
	}
	session := cookieNamed(set, auth.SessionCookie)
	if session == nil {
		t.Fatal("no session cookie after SSO sign-in")
	}
	if rec := s.do("GET", "/api/v1/me", nil, session); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"username":"mom"`) {
		t.Errorf("GET /me after SSO = %d %s", rec.Code, rec.Body)
	}

	// Open redirects are dropped.
	loc, _ = signIn(t, s, idp, "/api/v1/auth/oidc/start?client=web&redirect=//evil.example", "alice")
	if loc != "/" {
		t.Errorf("open redirect → %s", loc)
	}

	// Unlinking needs the password; afterwards SSO is refused again.
	wantCode(t, "unlink, wrong password", s.do("POST", "/api/v1/account/oidc/unlink", map[string]string{"password": "nope"}, web), 403, "invalid_password")
	wantCode(t, "unlink", s.do("POST", "/api/v1/account/oidc/unlink", map[string]string{"password": ownerPassword}, web), 204, "")
	wantCode(t, "unlink twice", s.do("POST", "/api/v1/account/oidc/unlink", map[string]string{"password": ownerPassword}, web), 409, "oidc_not_linked")
	if loc, _ := signIn(t, s, idp, "/api/v1/auth/oidc/start?client=web", "alice"); !strings.Contains(loc, "sso_error=not_linked") {
		t.Errorf("after unlink → %s", loc)
	}
}

func TestSSOStateIsBoundToBrowser(t *testing.T) {
	s, idp := newSSOServer(t)
	authURL, _ := redirectOf(t, "start", s.do("GET", "/api/v1/auth/oidc/start?client=web", nil))
	cb := strings.TrimPrefix(idp.authorize(authURL, "alice"), "http://chores.test")
	// Without the state cookie (e.g. a link sent to someone else) the callback is refused.
	if loc, _ := redirectOf(t, "callback", s.do("GET", cb, nil)); loc != "/login?sso_error=expired" {
		t.Errorf("callback without state cookie → %s", loc)
	}

	// An IdP error (user cancelled) comes back as denied.
	authURL, set := redirectOf(t, "start", s.do("GET", "/api/v1/auth/oidc/start?client=web", nil))
	state := cookieNamed(set, sso.StateCookie)
	u, _ := url.Parse(authURL)
	path := sso.CallbackPath + "?" + url.Values{"state": {u.Query().Get("state")}, "error": {"access_denied"}}.Encode()
	if loc, _ := redirectOf(t, "denied", s.do("GET", path, nil, state)); loc != "/login?sso_error=denied" {
		t.Errorf("IdP error → %s", loc)
	}

	// Flows expire.
	authURL, set = redirectOf(t, "start", s.do("GET", "/api/v1/auth/oidc/start?client=web", nil))
	cb = strings.TrimPrefix(idp.authorize(authURL, "alice"), "http://chores.test")
	s.clk.Advance(11 * time.Minute)
	if loc, _ := redirectOf(t, "late callback", s.do("GET", cb, nil, cookieNamed(set, sso.StateCookie))); loc != "/login?sso_error=expired" {
		t.Errorf("expired flow → %s", loc)
	}
}

func TestSSOAppExchange(t *testing.T) {
	s, idp := newSSOServer(t)
	web := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	signIn(t, s, idp, "/api/v1/account/oidc/link", "alice", web)

	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	appCode := func() string {
		t.Helper()
		loc, _ := signIn(t, s, idp, "/api/v1/auth/oidc/start?client=app&label=Pixel+9&challenge="+challenge, "alice")
		u, err := url.Parse(loc)
		if err != nil || u.Scheme != "chorecast" || u.Host != "auth" || u.Query().Get("code") == "" {
			t.Fatalf("app callback → %s", loc)
		}
		return u.Query().Get("code")
	}

	wantCode(t, "start without challenge", s.do("GET", "/api/v1/auth/oidc/start?client=app", nil), 400, "bad_request")
	wantCode(t, "start with unknown client", s.do("GET", "/api/v1/auth/oidc/start?client=tv", nil), 422, "validation_failed")

	// A wrong verifier burns the code.
	code := appCode()
	wantCode(t, "wrong verifier", s.do("POST", "/api/v1/auth/oidc/exchange", map[string]string{"code": code, "verifier": strings.Repeat("x", 50)}), 400, "oidc_invalid_code")
	wantCode(t, "code after wrong verifier", s.do("POST", "/api/v1/auth/oidc/exchange", map[string]string{"code": code, "verifier": verifier}), 400, "oidc_invalid_code")

	code = appCode()
	rec := s.do("POST", "/api/v1/auth/oidc/exchange", map[string]string{"code": code, "verifier": verifier})
	wantCode(t, "exchange", rec, 200, "")
	token := decode[struct{ Token string }](t, rec).Token
	if rec := s.bearer("GET", "/api/v1/me", token, nil); rec.Code != 200 {
		t.Fatalf("app token from SSO: %d %s", rec.Code, rec.Body)
	}
	wantCode(t, "reused code", s.do("POST", "/api/v1/auth/oidc/exchange", map[string]string{"code": code, "verifier": verifier}), 400, "oidc_invalid_code")

	// The session list shows the device label the app passed.
	if !strings.Contains(s.do("GET", "/api/v1/account/sessions", nil, web).Body.String(), `"label":"Pixel 9"`) {
		t.Error("app session label missing")
	}

	// Unlinked identities get an error back in the app.
	loc, _ := signIn(t, s, idp, "/api/v1/auth/oidc/start?client=app&challenge="+challenge, "bob")
	if loc != "chorecast://auth?error=not_linked" {
		t.Errorf("unlinked app sign-in → %s", loc)
	}

	// Codes expire.
	code = appCode()
	s.clk.Advance(3 * time.Minute)
	wantCode(t, "expired code", s.do("POST", "/api/v1/auth/oidc/exchange", map[string]string{"code": code, "verifier": verifier}), 400, "oidc_invalid_code")
}

func TestSSOLinkConflict(t *testing.T) {
	s, idp := newSSOServer(t)
	web := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	signIn(t, s, idp, "/api/v1/account/oidc/link", "alice", web)

	hash, _ := auth.HashSecret("dad password")
	if _, err := s.store.CreateUser(t.Context(), createParent("Dad", "dad", hash)); err != nil {
		t.Fatal(err)
	}
	dad := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": "dad", "password": "dad password"})))
	if loc, _ := signIn(t, s, idp, "/api/v1/account/oidc/link", "alice", dad); loc != "/account?sso_error=already_linked" {
		t.Errorf("linking a taken identity → %s", loc)
	}
}

func TestSSODisabled(t *testing.T) {
	s := newAuthServer(t)
	wantCode(t, "start", s.do("GET", "/api/v1/auth/oidc/start?client=web", nil), 404, "oidc_disabled")
	wantCode(t, "exchange", s.do("POST", "/api/v1/auth/oidc/exchange", map[string]string{"code": "x", "verifier": strings.Repeat("v", 43)}), 404, "oidc_disabled")
}

// The provider is discovered during the first request; key fetches later must not reuse that
// request's (by then cancelled) context.
func TestSSOProviderOutlivesFirstRequest(t *testing.T) {
	s, idp := newSSOServer(t)
	web := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))

	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/account/oidc/link", nil)
	req.AddCookie(web)
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	cancel()
	authURL, set := redirectOf(t, "start", rec)

	cb := strings.TrimPrefix(idp.authorize(authURL, "alice"), "http://chores.test")
	if loc, _ := redirectOf(t, "callback", s.do("GET", cb, nil, cookieNamed(set, sso.StateCookie))); loc != "/account?sso=linked" {
		t.Errorf("callback after the first request's context ended → %s", loc)
	}
}
