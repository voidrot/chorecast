package api_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/voidrot/chorecast/server/internal/auth"
)

const ownerPassword = "correct horse battery"

// newAuthServer is a set-up server whose owner is "mom" (password ownerPassword).
func newAuthServer(t *testing.T) *setupServer {
	t.Helper()
	s := newSetupServer(t)
	token, err := s.svc.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "setup", s.do("POST", "/api/v1/setup", setupRequest(token)), 201, "")
	return s
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Result().Header)
	return nil
}

func login(body map[string]any) map[string]any {
	out := map[string]any{"username": "mom", "password": ownerPassword, "client": "web"}
	for k, v := range body {
		out[k] = v
	}
	return out
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func (s *setupServer) bearer(method, path, token string, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequestWithContext(s.t.Context(), method, path, strings.NewReader(mustJSON(s.t, body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	return rec
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoginWebAndApp(t *testing.T) {
	s := newAuthServer(t)

	wantCode(t, "wrong password", s.do("POST", "/api/v1/auth/login", login(map[string]any{"password": "nope"})), 401, "invalid_credentials")
	wantCode(t, "unknown user", s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": "dad"})), 401, "invalid_credentials")
	wantCode(t, "kid has no password", s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": "sam"})), 401, "invalid_credentials")

	// Web: cookie session; usernames are case-insensitive.
	rec := s.do("POST", "/api/v1/auth/login", login(map[string]any{"username": " Mom "}))
	wantCode(t, "web login", rec, 200, "")
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Errorf("web login leaked a token in the body: %s", rec.Body)
	}
	web := sessionCookie(t, rec)
	me := s.do("GET", "/api/v1/me", nil, web)
	if !strings.Contains(me.Body.String(), `"username":"mom"`) || !strings.Contains(me.Body.String(), `"displayName":"Mom"`) {
		t.Errorf("GET /me = %s", me.Body)
	}

	// App: bearer token.
	rec = s.do("POST", "/api/v1/auth/login", login(map[string]any{"client": "app", "label": "Pixel 9"}))
	wantCode(t, "app login", rec, 200, "")
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("app login set cookies: %v", rec.Result().Cookies())
	}
	appToken := decode[struct{ Token string }](t, rec).Token
	if rec := s.bearer("GET", "/api/v1/me", appToken, nil); rec.Code != 200 {
		t.Fatalf("GET /me with app token = %d %s", rec.Code, rec.Body)
	}

	// The app token can't be used as a cookie, nor the session as a bearer token.
	asCookie := &http.Cookie{Name: auth.SessionCookie, Value: appToken} //nolint:gosec // test request cookie
	if rec := s.do("GET", "/api/v1/me", nil, asCookie); rec.Code != 401 {
		t.Errorf("app token as cookie = %d", rec.Code)
	}
	if rec := s.bearer("GET", "/api/v1/me", web.Value, nil); rec.Code != 401 {
		t.Errorf("web session as bearer = %d", rec.Code)
	}

	// Sessions list both; the web one is current. Revoking the app token signs it out.
	list := decode[struct {
		Items []struct {
			ID      int64
			Kind    string
			Label   string
			Current bool
		}
	}](t, s.do("GET", "/api/v1/account/sessions", nil, web))
	if len(list.Items) != 3 { // setup session + web + app
		t.Fatalf("sessions = %+v", list.Items)
	}
	var appID int64
	for _, it := range list.Items {
		if it.Kind == "app" {
			appID = it.ID
			if it.Label != "Pixel 9" || it.Current {
				t.Errorf("app session = %+v", it)
			}
		}
	}
	wantCode(t, "revoke app session", s.do("DELETE", "/api/v1/account/sessions/"+itoa(appID), nil, web), 204, "")
	wantCode(t, "revoked app token", s.bearer("GET", "/api/v1/me", appToken, nil), 401, "unauthenticated")
	wantCode(t, "revoke unknown session", s.do("DELETE", "/api/v1/account/sessions/999", nil, web), 404, "not_found")

	// Logout revokes the session and clears the cookie.
	rec = s.do("POST", "/api/v1/auth/logout", nil, web)
	wantCode(t, "logout", rec, 204, "")
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("logout cookies = %+v", c)
	}
	wantCode(t, "after logout", s.do("GET", "/api/v1/me", nil, web), 401, "unauthenticated")
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestLoginRateLimit(t *testing.T) {
	s := newAuthServer(t)
	for range 5 {
		wantCode(t, "wrong password", s.do("POST", "/api/v1/auth/login", login(map[string]any{"password": "nope"})), 401, "invalid_credentials")
	}
	rec := s.do("POST", "/api/v1/auth/login", login(nil))
	wantCode(t, "locked out (even with the right password)", rec, 429, "rate_limited")
	if ra := rec.Header().Get("Retry-After"); ra != "900" {
		t.Errorf("Retry-After = %q, want 900", ra)
	}
	s.clk.Advance(15 * time.Minute)
	wantCode(t, "after the window", s.do("POST", "/api/v1/auth/login", login(nil)), 200, "")
}

func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTOTPAndRecoveryCodes(t *testing.T) {
	s := newAuthServer(t)
	web := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))

	wantCode(t, "enable before setup", s.do("POST", "/api/v1/account/totp/enable", map[string]string{"code": "123456"}, web), 409, "totp_setup_not_started")
	rec := s.do("POST", "/api/v1/account/totp/setup", nil, web)
	wantCode(t, "begin totp", rec, 200, "")
	setup := decode[struct{ Secret, OtpauthURL string }](t, rec)
	if !strings.Contains(setup.OtpauthURL, "Chorecast%20%28The%20Bradys%29") {
		t.Errorf("otpauth URL = %s", setup.OtpauthURL)
	}
	wantCode(t, "enable with wrong code", s.do("POST", "/api/v1/account/totp/enable", map[string]string{"code": "000000"}, web), 422, "invalid_totp_code")
	rec = s.do("POST", "/api/v1/account/totp/enable", map[string]string{"code": totpCode(t, setup.Secret, s.clk.Now())}, web)
	wantCode(t, "enable totp", rec, 200, "")
	codes := decode[struct{ RecoveryCodes []string }](t, rec).RecoveryCodes
	if len(codes) != 10 {
		t.Fatalf("recovery codes = %v", codes)
	}
	if !strings.Contains(s.do("GET", "/api/v1/me", nil, web).Body.String(), `"totpEnabled":true`) {
		t.Error("/me doesn't report totpEnabled")
	}

	// A password alone is no longer enough.
	wantCode(t, "password only", s.do("POST", "/api/v1/auth/login", login(nil)), 401, "totp_required")
	wantCode(t, "wrong code", s.do("POST", "/api/v1/auth/login", login(map[string]any{"totpCode": "000000"})), 401, "invalid_totp_code")

	// The enrolment code's step is used; the next step's code works once.
	s.clk.Advance(30 * time.Second)
	code := totpCode(t, setup.Secret, s.clk.Now())
	wantCode(t, "login with code", s.do("POST", "/api/v1/auth/login", login(map[string]any{"totpCode": code})), 200, "")
	wantCode(t, "replayed code", s.do("POST", "/api/v1/auth/login", login(map[string]any{"totpCode": code})), 401, "invalid_totp_code")

	// Recovery codes work once each, typed loosely.
	rec = s.do("POST", "/api/v1/auth/login", login(map[string]any{"recoveryCode": strings.ToUpper(codes[0])}))
	wantCode(t, "login with recovery code", rec, 200, "")
	if left := decode[struct{ RecoveryCodesLeft int }](t, rec).RecoveryCodesLeft; left != 9 {
		t.Errorf("recoveryCodesLeft = %d", left)
	}
	wantCode(t, "reused recovery code", s.do("POST", "/api/v1/auth/login", login(map[string]any{"recoveryCode": codes[0]})), 401, "invalid_totp_code")

	// Regenerating invalidates the old set.
	wantCode(t, "regenerate with wrong password", s.do("POST", "/api/v1/account/recovery-codes", map[string]string{"password": "nope"}, web), 403, "invalid_password")
	rec = s.do("POST", "/api/v1/account/recovery-codes", map[string]string{"password": ownerPassword}, web)
	wantCode(t, "regenerate", rec, 200, "")
	fresh := decode[struct{ RecoveryCodes []string }](t, rec).RecoveryCodes
	wantCode(t, "old recovery code", s.do("POST", "/api/v1/auth/login", login(map[string]any{"recoveryCode": codes[1]})), 401, "invalid_totp_code")
	wantCode(t, "new recovery code", s.do("POST", "/api/v1/auth/login", login(map[string]any{"recoveryCode": fresh[0]})), 200, "")

	// Disabling needs the password; afterwards a password is enough again.
	wantCode(t, "disable with wrong password", s.do("POST", "/api/v1/account/totp/disable", map[string]string{"password": "nope"}, web), 403, "invalid_password")
	wantCode(t, "disable", s.do("POST", "/api/v1/account/totp/disable", map[string]string{"password": ownerPassword}, web), 204, "")
	wantCode(t, "password only after disable", s.do("POST", "/api/v1/auth/login", login(nil)), 200, "")
	wantCode(t, "disable twice", s.do("POST", "/api/v1/account/totp/disable", map[string]string{"password": ownerPassword}, web), 409, "totp_not_enabled")
}

func TestChangePasswordAndPIN(t *testing.T) {
	s := newAuthServer(t)
	web := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	other := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))

	wantCode(t, "wrong current password", s.do("PUT", "/api/v1/account/password",
		map[string]string{"currentPassword": "nope", "newPassword": "new password 1"}, web), 403, "invalid_password")
	wantCode(t, "too short", s.do("PUT", "/api/v1/account/password",
		map[string]string{"currentPassword": ownerPassword, "newPassword": "short"}, web), 422, "validation_failed")
	wantCode(t, "change password", s.do("PUT", "/api/v1/account/password",
		map[string]string{"currentPassword": ownerPassword, "newPassword": "new password 1"}, web), 204, "")
	wantCode(t, "this session survives", s.do("GET", "/api/v1/me", nil, web), 200, "")
	wantCode(t, "other sessions are signed out", s.do("GET", "/api/v1/me", nil, other), 401, "unauthenticated")
	wantCode(t, "old password", s.do("POST", "/api/v1/auth/login", login(nil)), 401, "invalid_credentials")
	wantCode(t, "new password", s.do("POST", "/api/v1/auth/login", login(map[string]any{"password": "new password 1"})), 200, "")

	wantCode(t, "bad pin", s.do("PUT", "/api/v1/account/pin", map[string]string{"password": "new password 1", "pin": "12a"}, web), 422, "validation_failed")
	wantCode(t, "set pin", s.do("PUT", "/api/v1/account/pin", map[string]string{"password": "new password 1", "pin": "4321"}, web), 204, "")
	u, err := s.store.GetParentByUsername(t.Context(), sqlNull("mom"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := auth.VerifySecret("4321", u.ParentPinHash.String); !ok {
		t.Error("PIN not stored")
	}
	if !strings.Contains(s.do("GET", "/api/v1/me", nil, web).Body.String(), `"pinSet":true`) {
		t.Error("/me doesn't report pinSet")
	}
	wantCode(t, "clear pin", s.do("PUT", "/api/v1/account/pin", map[string]string{"password": "new password 1", "pin": ""}, web), 204, "")
	if !strings.Contains(s.do("GET", "/api/v1/me", nil, web).Body.String(), `"pinSet":false`) {
		t.Error("PIN not cleared")
	}
}

// Kids and devices can't reach account management.
func TestAccountEndpointsAreParentOnly(t *testing.T) {
	s := newAuthServer(t)
	kid, err := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.NewStore(s.store.Queries, s.clk).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDevice, UserID: kid.ID})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "kid lists sessions", s.bearer("GET", "/api/v1/account/sessions", token, nil), 403, "insufficient_scope")
	// Logging out would unpair the device; devices are revoked from the devices page instead.
	wantCode(t, "kid logs out", s.bearer("POST", "/api/v1/auth/logout", token, nil), 403, "insufficient_scope")
}

func sqlNull(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
