package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/sso"
)

// invite creates an invite as the given session and returns its token.
func invite(t *testing.T, s *setupServer, as *http.Cookie, role string) (int64, string) {
	t.Helper()
	rec := s.do("POST", "/api/v1/invites", map[string]string{"role": role}, as)
	wantCode(t, "create invite", rec, 200, "")
	inv := decode[struct {
		ID  int64
		URL string
	}](t, rec)
	u, err := url.Parse(inv.URL)
	if err != nil || u.Path != "/invite" || u.Query().Get("token") == "" {
		t.Fatalf("invite url = %q", inv.URL)
	}
	return inv.ID, u.Query().Get("token")
}

func accept(s *setupServer, token, name, username, password string) *httptest.ResponseRecorder {
	return s.do("POST", "/api/v1/auth/invite/accept", map[string]string{
		"token": token, "displayName": name, "username": username, "password": password,
	})
}

// newHousehold: owner mom (web session) and parent dad (joined by invite, web session).
func newHousehold(t *testing.T) (s *setupServer, mom, dad *http.Cookie, dadID int64) {
	t.Helper()
	s = newAuthServer(t)
	mom = sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	_, token := invite(t, s, mom, "parent")
	rec := accept(s, token, "Dad", "dad", "dad password")
	wantCode(t, "accept", rec, 200, "")
	return s, mom, sessionCookie(t, rec), decode[struct{ UserID int64 }](t, rec).UserID
}

func TestInviteLifecycle(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	_, token := invite(t, s, mom, "parent")

	rec := s.do("POST", "/api/v1/auth/invite", map[string]string{"token": token})
	wantCode(t, "invite info", rec, 200, "")
	if b := rec.Body.String(); !strings.Contains(b, `"householdName":"The Bradys"`) || !strings.Contains(b, `"invitedBy":"Mom"`) || !strings.Contains(b, `"role":"parent"`) {
		t.Errorf("invite info = %s", b)
	}
	wantCode(t, "bogus invite", s.do("POST", "/api/v1/auth/invite", map[string]string{"token": "nope"}), 410, "invalid_invite")
	wantCode(t, "taken username", accept(s, token, "Dad", "mom", "dad password"), 409, "username_taken")
	wantCode(t, "bad username, bogus token", accept(s, "nope", "Dad", "Dad!", "dad password"), 422, "validation_failed")

	rec = accept(s, token, " Dad ", "dad", "dad password")
	wantCode(t, "accept", rec, 200, "")
	dad := sessionCookie(t, rec)
	if me := s.do("GET", "/api/v1/me", nil, dad).Body.String(); !strings.Contains(me, `"displayName":"Dad"`) || !strings.Contains(me, `"owner":false`) {
		t.Errorf("new parent /me = %s", me)
	}
	wantCode(t, "invite reused", accept(s, token, "Eve", "eve", "eve password"), 410, "invalid_invite")
	wantCode(t, "new parent logs in", s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": "dad", "password": "dad password"})), 200, "")

	// Pending list, revoke, expiry.
	id, revoked := invite(t, s, mom, "owner")
	list := s.do("GET", "/api/v1/invites", nil, mom)
	if !strings.Contains(list.Body.String(), `"role":"owner"`) || strings.Contains(list.Body.String(), `"role":"parent"`) {
		t.Errorf("pending invites = %s", list.Body)
	}
	wantCode(t, "revoke", s.do("DELETE", "/api/v1/invites/"+itoa(id), nil, mom), 204, "")
	wantCode(t, "revoked invite", accept(s, revoked, "Eve", "eve", "eve password"), 410, "invalid_invite")
	wantCode(t, "revoke twice", s.do("DELETE", "/api/v1/invites/"+itoa(id), nil, mom), 404, "not_found")

	_, late := invite(t, s, mom, "owner")
	s.clk.Advance(7*24*time.Hour + time.Minute)
	wantCode(t, "expired invite", accept(s, late, "Eve", "eve", "eve password"), 410, "invalid_invite")

	// An owner invite makes an owner.
	_, owner := invite(t, s, mom, "owner")
	rec = accept(s, owner, "Gran", "gran", "gran password")
	wantCode(t, "accept owner invite", rec, 200, "")
	if !strings.Contains(s.do("GET", "/api/v1/me", nil, sessionCookie(t, rec)).Body.String(), `"owner":true`) {
		t.Error("owner invite didn't make an owner")
	}
}

// Every owner-only capability is refused for a parent who isn't an owner.
func TestOwnerOnlyCapabilities(t *testing.T) {
	s, mom, dad, _ := newHousehold(t)
	momID := decode[struct{ UserID int64 }](t, s.do("GET", "/api/v1/me", nil, mom)).UserID
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/invites"},
		{"GET", "/api/v1/invites"},
		{"DELETE", "/api/v1/invites/1"},
		{"PUT", "/api/v1/parents/" + itoa(momID) + "/role"},
		{"DELETE", "/api/v1/parents/" + itoa(momID)},
		{"POST", "/api/v1/parents/" + itoa(momID) + "/reset-password"},
		{"POST", "/api/v1/parents/" + itoa(momID) + "/disable-totp"},
	} {
		wantCode(t, "parent: "+c.method+" "+c.path, s.do(c.method, c.path, map[string]any{"owner": false, "role": "parent"}, dad), 403, "insufficient_scope")
	}
	// Any parent can see who the parents are.
	if rec := s.do("GET", "/api/v1/parents", nil, dad); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"displayName":"Mom"`) {
		t.Errorf("parent lists parents = %d %s", rec.Code, rec.Body)
	}
}

func TestRolesKeepAnOwner(t *testing.T) {
	s, mom, dad, dadID := newHousehold(t)
	momID := decode[struct{ UserID int64 }](t, s.do("GET", "/api/v1/me", nil, mom)).UserID

	wantCode(t, "demote last owner", s.do("PUT", "/api/v1/parents/"+itoa(momID)+"/role", map[string]bool{"owner": false}, mom), 409, "last_owner")
	wantCode(t, "remove last owner", s.do("DELETE", "/api/v1/parents/"+itoa(momID), nil, mom), 409, "last_owner")

	rec := s.do("PUT", "/api/v1/parents/"+itoa(dadID)+"/role", map[string]bool{"owner": true}, mom)
	wantCode(t, "promote", rec, 200, "")
	if !strings.Contains(rec.Body.String(), `"owner":true`) {
		t.Errorf("promote = %s", rec.Body)
	}
	// Scopes follow the role on the next request.
	wantCode(t, "new owner invites", s.do("POST", "/api/v1/invites", map[string]string{"role": "parent"}, dad), 200, "")
	wantCode(t, "now mom can step down", s.do("PUT", "/api/v1/parents/"+itoa(momID)+"/role", map[string]bool{"owner": false}, mom), 200, "")
	wantCode(t, "demoted mom can't invite", s.do("POST", "/api/v1/invites", map[string]string{"role": "parent"}, mom), 403, "insufficient_scope")
	wantCode(t, "unknown parent", s.do("PUT", "/api/v1/parents/999/role", map[string]bool{"owner": true}, dad), 404, "parent_not_found")
}

func TestRemoveParent(t *testing.T) {
	s, mom, dad, dadID := newHousehold(t)
	app := decode[struct{ Token string }](t, s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": "dad", "password": "dad password", "client": "app"}))).Token

	wantCode(t, "remove", s.do("DELETE", "/api/v1/parents/"+itoa(dadID), nil, mom), 204, "")
	wantCode(t, "removed web session", s.do("GET", "/api/v1/me", nil, dad), 401, "unauthenticated")
	wantCode(t, "removed app token", s.bearer("GET", "/api/v1/me", app, nil), 401, "unauthenticated")
	wantCode(t, "removed can't log in", s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": "dad", "password": "dad password"})), 401, "invalid_credentials")
	if strings.Contains(s.do("GET", "/api/v1/parents", nil, mom).Body.String(), "Dad") {
		t.Error("removed parent still listed")
	}
	// The row stays for history; the username is free again.
	u, err := s.store.GetUser(t.Context(), dadID)
	if err != nil || u.DisplayName != "Dad" || !u.RemovedAt.Valid || u.Username.Valid || u.PasswordHash.Valid {
		t.Errorf("removed row = %+v, %v", u, err)
	}
	_, token := invite(t, s, mom, "parent")
	wantCode(t, "username reusable", accept(s, token, "Dad", "dad", "new dad password"), 200, "")
	wantCode(t, "remove twice", s.do("DELETE", "/api/v1/parents/"+itoa(dadID), nil, mom), 404, "parent_not_found")
}

func TestOwnerResetsParent(t *testing.T) {
	s, mom, dad, dadID := newHousehold(t)

	// Dad turns on 2FA, then loses his phone.
	secret := decode[struct{ Secret string }](t, s.do("POST", "/api/v1/account/totp/setup", nil, dad)).Secret
	wantCode(t, "enable", s.do("POST", "/api/v1/account/totp/enable", map[string]string{"code": totpCode(t, secret, s.clk.Now())}, dad), 200, "")
	dadLogin := map[string]any{"username": "dad", "password": "dad password"}
	wantCode(t, "needs code", s.do("POST", "/api/v1/auth/login", login(dadLogin)), 401, "totp_required")
	wantCode(t, "owner disables 2fa", s.do("POST", "/api/v1/parents/"+itoa(dadID)+"/disable-totp", nil, mom), 204, "")
	wantCode(t, "password alone works", s.do("POST", "/api/v1/auth/login", login(dadLogin)), 200, "")

	// And forgets his password.
	rec := s.do("POST", "/api/v1/parents/"+itoa(dadID)+"/reset-password", nil, mom)
	wantCode(t, "reset password", rec, 200, "")
	temp := decode[struct{ Password string }](t, rec).Password
	wantCode(t, "sessions revoked", s.do("GET", "/api/v1/me", nil, dad), 401, "unauthenticated")
	wantCode(t, "old password", s.do("POST", "/api/v1/auth/login", login(dadLogin)), 401, "invalid_credentials")
	wantCode(t, "temporary password", s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": "dad", "password": temp})), 200, "")
}

func TestJoinWithSSO(t *testing.T) {
	s, idp := newSSOServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	_, token := invite(t, s, mom, "parent")
	if !strings.Contains(s.do("POST", "/api/v1/auth/invite", map[string]string{"token": token}).Body.String(), `"oidc":true`) {
		t.Error("invite info doesn't report oidc")
	}
	wantCode(t, "sso start with bogus invite", s.do("GET", "/api/v1/auth/oidc/start?client=invite&invite=nope&displayName=Dad", nil), 410, "invalid_invite")

	start := "/api/v1/auth/oidc/start?" + url.Values{"client": {"invite"}, "invite": {token}, "displayName": {"Dad"}}.Encode()
	loc, set := signIn(t, s, idp, start, "dad-sub")
	session := cookieNamed(set, auth.SessionCookie)
	if loc != "/" || session == nil {
		t.Fatalf("join with SSO → %s (session %v)", loc, session)
	}
	me := s.do("GET", "/api/v1/me", nil, session).Body.String()
	if !strings.Contains(me, `"displayName":"Dad"`) || !strings.Contains(me, `"oidcLinked":true`) || !strings.Contains(me, `"hasPassword":false`) {
		t.Errorf("SSO parent /me = %s", me)
	}
	// Signs in again with SSO; sets a first password without a current one.
	if loc, _ := signIn(t, s, idp, "/api/v1/auth/oidc/start?client=web", "dad-sub"); loc != "/" {
		t.Errorf("SSO sign-in → %s", loc)
	}
	wantCode(t, "first password", s.do("PUT", "/api/v1/account/password", map[string]string{"newPassword": "dad password"}, session), 204, "")
	wantCode(t, "second change needs current", s.do("PUT", "/api/v1/account/password", map[string]string{"newPassword": "dad password 2"}, session), 403, "invalid_password")

	// The invite is used up: refused before even going to the IdP.
	wantCode(t, "reusing an invite via SSO", s.do("GET", start, nil), 410, "invalid_invite")

	// An invite used up while the invitee was at the IdP comes back as an error on the invite page.
	_, token2 := invite(t, s, mom, "parent")
	start2 := "/api/v1/auth/oidc/start?" + url.Values{"client": {"invite"}, "invite": {token2}, "displayName": {"Eve"}}.Encode()
	authURL, set2 := redirectOf(t, "start", s.do("GET", start2, nil))
	wantCode(t, "someone else accepts first", accept(s, token2, "Gran", "gran", "gran password"), 200, "")
	cb := strings.TrimPrefix(idp.authorize(authURL, "eve-sub"), "http://chores.test")
	if loc, _ := redirectOf(t, "late callback", s.do("GET", cb, nil, cookieNamed(set2, sso.StateCookie))); loc != "/invite?sso_error=invalid_invite" {
		t.Errorf("invite used meanwhile → %s", loc)
	}
}
