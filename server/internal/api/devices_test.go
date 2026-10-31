package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/config"
)

type pairingJSON struct {
	Code, Server, QR string
	KidID            int64
}

func pair(t *testing.T, s *setupServer, as *http.Cookie, kid string) pairingJSON {
	t.Helper()
	body := map[string]string{}
	if kid != "" {
		body["kid"] = kid
	}
	rec := s.do("POST", "/api/v1/devices/pairing", body, as)
	wantCode(t, "create pairing", rec, 200, "")
	return decode[pairingJSON](t, rec)
}

func pairDevice(t *testing.T, s *setupServer, code string) (int64, string) {
	t.Helper()
	rec := s.do("POST", "/api/v1/devices/pair", map[string]string{"code": code, "name": "Pixel Tablet", "appVersion": "0.1.0"})
	wantCode(t, "pair", rec, 200, "")
	out := decode[struct {
		Device struct{ ID int64 }
		Token  string
	}](t, rec)
	return out.Device.ID, out.Token
}

func TestPairKidDevice(t *testing.T) {
	s := newAuthServerWith(t, configWithPublicURL())
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))

	p := pair(t, s, mom, "sam")
	if p.Server != "http://chores.test" || p.QR != `{"server":"http://chores.test","code":"`+p.Code+`"}` || p.KidID == 0 || len(p.Code) != 9 {
		t.Fatalf("pairing = %+v", p)
	}
	wantCode(t, "wrong code", s.do("POST", "/api/v1/devices/pair", map[string]string{"code": "aaaa-aaaa"}), 400, "invalid_pairing_code")

	// Typed loosely: case and hyphen don't matter.
	_, token := pairDevice(t, s, strings.ToUpper(strings.ReplaceAll(p.Code, "-", "")))
	me := s.bearer("GET", "/api/v1/me", token, nil).Body.String()
	if !strings.Contains(me, `"kind":"kid"`) || !strings.Contains(me, `"displayName":"Sam"`) || !strings.Contains(me, `"deviceId"`) {
		t.Errorf("kid device /me = %s", me)
	}
	wantCode(t, "code is single use", s.do("POST", "/api/v1/devices/pair", map[string]string{"code": p.Code}), 400, "invalid_pairing_code")

	// A kid's device has no picker kids and can't start kid sessions.
	if b := s.bearer("GET", "/api/v1/devices/picker", token, nil).Body.String(); !strings.Contains(b, `"kids":[]`) || !strings.Contains(b, `"shared":false`) {
		t.Errorf("kid device picker = %s", b)
	}
	wantCode(t, "kid device starts a session", s.bearer("POST", "/api/v1/devices/session", token, map[string]string{"kid": "sam-2"}), 403, "insufficient_scope")

	// Codes expire after 5 minutes.
	late := pair(t, s, mom, "")
	s.clk.Advance(5*time.Minute + time.Second)
	wantCode(t, "expired code", s.do("POST", "/api/v1/devices/pair", map[string]string{"code": late.Code}), 400, "invalid_pairing_code")

	// Archiving the kid revokes their device.
	wantCode(t, "archive", s.do("POST", "/api/v1/kids/sam/archive", nil, mom), 200, "")
	wantCode(t, "archived kid's device", s.bearer("GET", "/api/v1/me", token, nil), 401, "unauthenticated")
	wantCode(t, "pairing for an archived kid", s.do("POST", "/api/v1/devices/pairing", map[string]string{"kid": "sam"}, mom), 409, "conflict")
	wantCode(t, "unknown kid", s.do("POST", "/api/v1/devices/pairing", map[string]string{"kid": "nobody"}, mom), 404, "kid_not_found")
}

func TestSharedDeviceSessions(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	wantCode(t, "sam needs a PIN", s.do("PUT", "/api/v1/kids/sam", map[string]any{"displayName": "Sam", "color": "#3b82f6", "pinRequired": true}, mom), 200, "")
	wantCode(t, "sam's PIN", s.do("PUT", "/api/v1/kids/sam/pin", map[string]string{"pin": "2468"}, mom), 204, "")
	wantCode(t, "mom's parent PIN", s.do("PUT", "/api/v1/account/pin", map[string]string{"password": ownerPassword, "pin": "1357"}, mom), 204, "")

	deviceID, device := pairDevice(t, s, pair(t, s, mom, "").Code)
	if me := s.bearer("GET", "/api/v1/me", device, nil).Body.String(); !strings.Contains(me, `"kind":"shared_device"`) {
		t.Fatalf("shared device /me = %s", me)
	}
	picker := decode[struct {
		Device struct {
			ID     int64
			Shared bool
		}
		Kids []struct {
			Slug        string
			PINRequired bool
		}
		Parents []struct {
			ID          int64
			DisplayName string
		}
	}](t, s.bearer("GET", "/api/v1/devices/picker", device, nil))
	if picker.Device.ID != deviceID || !picker.Device.Shared || len(picker.Kids) != 2 || len(picker.Parents) != 1 || picker.Parents[0].DisplayName != "Mom" {
		t.Fatalf("picker = %+v", picker)
	}
	for _, k := range picker.Kids {
		if (k.Slug == "sam") != k.PINRequired {
			t.Errorf("pinRequired for %s = %v", k.Slug, k.PINRequired)
		}
	}

	// Kid sessions: sam needs the PIN, sam-2 doesn't.
	wantCode(t, "sam without PIN", s.bearer("POST", "/api/v1/devices/session", device, map[string]string{"kid": "sam"}), 403, "invalid_pin")
	rec := s.bearer("POST", "/api/v1/devices/session", device, map[string]string{"kid": "sam", "pin": "2468"})
	wantCode(t, "sam with PIN", rec, 200, "")
	samSession := decode[struct{ Token string }](t, rec).Token
	if me := s.bearer("GET", "/api/v1/me", samSession, nil).Body.String(); !strings.Contains(me, `"kind":"kid"`) || !strings.Contains(me, `"displayName":"Sam"`) {
		t.Errorf("kid session /me = %s", me)
	}
	wantCode(t, "sam-2 without PIN", s.bearer("POST", "/api/v1/devices/session", device, map[string]string{"kid": "sam-2"}), 200, "")
	// A kid session can't open another kid's session.
	wantCode(t, "session from a session", s.bearer("POST", "/api/v1/devices/session", samSession, map[string]string{"kid": "sam-2"}), 403, "insufficient_scope")

	// Locking ends the session but keeps the device paired.
	wantCode(t, "lock", s.bearer("POST", "/api/v1/devices/lock", samSession, nil), 204, "")
	wantCode(t, "locked session", s.bearer("GET", "/api/v1/me", samSession, nil), 401, "unauthenticated")
	wantCode(t, "lock with device token is a no-op", s.bearer("POST", "/api/v1/devices/lock", device, nil), 204, "")
	wantCode(t, "device still paired", s.bearer("GET", "/api/v1/me", device, nil), 200, "")

	// Kid PIN lockout: 5 wrong PINs for sam on this device.
	for range 5 {
		wantCode(t, "wrong kid PIN", s.bearer("POST", "/api/v1/devices/session", device, map[string]string{"kid": "sam", "pin": "0000"}), 403, "invalid_pin")
	}
	wantCode(t, "kid PIN locked", s.bearer("POST", "/api/v1/devices/session", device, map[string]string{"kid": "sam", "pin": "2468"}), 429, "pin_locked")
	wantCode(t, "other kid unaffected", s.bearer("POST", "/api/v1/devices/session", device, map[string]string{"kid": "sam-2"}), 200, "")
}

func TestParentElevation(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	momID := decode[struct{ UserID int64 }](t, s.do("GET", "/api/v1/me", nil, mom)).UserID
	wantCode(t, "mom's parent PIN", s.do("PUT", "/api/v1/account/pin", map[string]string{"password": ownerPassword, "pin": "1357"}, mom), 204, "")
	_, device := pairDevice(t, s, pair(t, s, mom, "").Code)
	elevate := func(pin string) *httptest.ResponseRecorder {
		return s.bearer("POST", "/api/v1/devices/elevate", device, map[string]any{"parentId": momID, "pin": pin})
	}

	rec := elevate("1357")
	wantCode(t, "elevate", rec, 200, "")
	parent := decode[struct{ Token, ExpiresAt string }](t, rec).Token
	me := s.bearer("GET", "/api/v1/me", parent, nil).Body.String()
	if !strings.Contains(me, `"kind":"parent"`) || !strings.Contains(me, `"elevated":true`) || !strings.Contains(me, `"owner":false`) ||
		!strings.Contains(me, "chores:review") || strings.Contains(me, "account:manage") || strings.Contains(me, "server:manage") {
		t.Errorf("elevated /me = %s", me)
	}
	wantCode(t, "elevated parent can't touch accounts", s.bearer("GET", "/api/v1/account/sessions", parent, nil), 403, "insufficient_scope")
	wantCode(t, "elevated parent can manage kids", s.bearer("GET", "/api/v1/kids?archived=true", parent, nil), 200, "")
	s.clk.Advance(2*time.Minute + time.Second)
	wantCode(t, "elevation expires", s.bearer("GET", "/api/v1/me", parent, nil), 401, "unauthenticated")

	// Five wrong PINs lock elevation on this device for 15 minutes, even for the right PIN.
	for i := range 4 {
		wantCode(t, "wrong parent PIN "+itoa(int64(i)), elevate("0000"), 403, "invalid_pin")
	}
	rec = elevate("0000")
	wantCode(t, "fifth wrong PIN locks", rec, 429, "pin_locked")
	if rec.Header().Get("Retry-After") != "900" {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	wantCode(t, "locked even with the right PIN", elevate("1357"), 429, "pin_locked")
	s.clk.Advance(15 * time.Minute)
	wantCode(t, "unlocked after 15 minutes", elevate("1357"), 200, "")

	// A parent without a PIN can't elevate (and it counts as a failure).
	wantCode(t, "unknown parent", s.bearer("POST", "/api/v1/devices/elevate", device, map[string]any{"parentId": 999, "pin": "1357"}), 403, "invalid_pin")
	// A web session isn't a device.
	wantCode(t, "elevate from the web", s.do("POST", "/api/v1/devices/elevate", map[string]any{"parentId": momID, "pin": "1357"}, mom), 403, "not_a_device")
}

func configWithPublicURL() config.Config { return config.Config{PublicURL: "http://chores.test"} }

func TestDevicesPage(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	_, kidDevice := pairDevice(t, s, pair(t, s, mom, "sam").Code)
	tabletID, tablet := pairDevice(t, s, pair(t, s, mom, "").Code)

	session := decode[struct{ Token string }](t, s.bearer("POST", "/api/v1/devices/session", tablet, map[string]string{"kid": "sam-2"})).Token
	s.clk.Advance(2 * time.Minute)
	wantCode(t, "picker reports version", s.bearer("GET", "/api/v1/devices/picker?appVersion=0.2.0", tablet, nil), 200, "")

	type listed struct {
		ID          int64
		Name        string
		Shared      bool
		KidName     string
		AppVersion  string
		LastSeen    time.Time
		SessionUser string
	}
	list := decode[struct{ Items []listed }](t, s.do("GET", "/api/v1/devices", nil, mom)).Items
	if len(list) != 2 {
		t.Fatalf("devices = %+v", list)
	}
	byShared := map[bool]listed{list[0].Shared: list[0], list[1].Shared: list[1]}
	tab, kid := byShared[true], byShared[false]
	if tab.ID != tabletID || tab.AppVersion != "0.2.0" || tab.SessionUser != "Sam" || !tab.LastSeen.Equal(s.clk.Now().Truncate(time.Millisecond)) {
		t.Errorf("tablet = %+v (now %v)", tab, s.clk.Now())
	}
	if kid.KidName != "Sam" || kid.SessionUser != "" || kid.AppVersion != "0.1.0" {
		t.Errorf("kid device = %+v", kid)
	}

	// Kids can't manage devices.
	wantCode(t, "kid lists devices", s.bearer("GET", "/api/v1/devices", kidDevice, nil), 403, "insufficient_scope")

	// Remote lock ends the session; the tablet stays paired.
	rec := s.do("POST", "/api/v1/devices/"+itoa(tabletID)+"/lock", nil, mom)
	wantCode(t, "remote lock", rec, 200, "")
	if n := decode[struct{ EndedSessions int }](t, rec).EndedSessions; n != 1 {
		t.Errorf("endedSessions = %d", n)
	}
	wantCode(t, "session after remote lock", s.bearer("GET", "/api/v1/me", session, nil), 401, "unauthenticated")
	wantCode(t, "tablet still paired", s.bearer("GET", "/api/v1/devices/picker", tablet, nil), 200, "")
	if decode[struct{ Items []listed }](t, s.do("GET", "/api/v1/devices", nil, mom)).Items[0].SessionUser != "" {
		t.Error("session still listed after remote lock")
	}

	// Revoke unpairs at once, sessions included.
	session = decode[struct{ Token string }](t, s.bearer("POST", "/api/v1/devices/session", tablet, map[string]string{"kid": "sam-2"})).Token
	wantCode(t, "revoke", s.do("DELETE", "/api/v1/devices/"+itoa(tabletID), nil, mom), 204, "")
	wantCode(t, "revoked device token", s.bearer("GET", "/api/v1/devices/picker", tablet, nil), 401, "unauthenticated")
	wantCode(t, "revoked device's session", s.bearer("GET", "/api/v1/me", session, nil), 401, "unauthenticated")
	wantCode(t, "revoke twice", s.do("DELETE", "/api/v1/devices/"+itoa(tabletID), nil, mom), 404, "not_found")
	wantCode(t, "lock unknown", s.do("POST", "/api/v1/devices/999/lock", nil, mom), 404, "not_found")
	if n := len(decode[struct{ Items []listed }](t, s.do("GET", "/api/v1/devices", nil, mom)).Items); n != 1 {
		t.Errorf("devices after revoke = %d", n)
	}
}
