package api_test

import (
	"strings"
	"testing"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/config"
)

type householdJSON struct {
	Name                 string
	Timezone             string
	DayEnd               string
	WeekStart            string
	AllowNegativeBalance bool
	SiblingVisibility    string
	MediaRetentionDays   int
	AutoApproveHours     *int
	OfflineGraceMinutes  int
	DefaultChoreMinutes  int
}

func settings(extra map[string]any) map[string]any {
	m := map[string]any{
		"name": "The Bradys", "timezone": "America/Chicago", "dayEnd": "21:00", "weekStart": "sunday",
		"allowNegativeBalance": false, "siblingVisibility": "private", "mediaRetentionDays": 30, "offlineGraceMinutes": 120,
		"defaultChoreMinutes": 10,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestHouseholdSettings(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))

	got := decode[householdJSON](t, s.do("GET", "/api/v1/household", nil, mom))
	if got.Name != "The Bradys" || got.Timezone != "America/Chicago" || got.DayEnd != "21:00" || got.WeekStart != "sunday" ||
		got.SiblingVisibility != "private" || got.MediaRetentionDays != 30 || got.OfflineGraceMinutes != 120 || got.DefaultChoreMinutes != 10 ||
		got.AutoApproveHours != nil || got.AllowNegativeBalance {
		t.Errorf("defaults = %+v", got)
	}

	rec := s.do("PUT", "/api/v1/household", settings(map[string]any{
		"name": " Brady Bunch ", "allowNegativeBalance": true, "siblingVisibility": "leaderboard",
		"mediaRetentionDays": 90, "autoApproveHours": 48, "offlineGraceMinutes": 60, "defaultChoreMinutes": 15,
	}), mom)
	wantCode(t, "update", rec, 200, "")
	got = decode[householdJSON](t, rec)
	if got.Name != "Brady Bunch" || !got.AllowNegativeBalance || got.SiblingVisibility != "leaderboard" ||
		got.MediaRetentionDays != 90 || got.AutoApproveHours == nil || *got.AutoApproveHours != 48 || got.OfflineGraceMinutes != 60 || got.DefaultChoreMinutes != 15 {
		t.Errorf("updated = %+v", got)
	}
	// Leaving autoApproveHours out means never.
	if got := decode[householdJSON](t, s.do("PUT", "/api/v1/household", settings(nil), mom)); got.AutoApproveHours != nil {
		t.Errorf("auto-approve not cleared: %+v", got)
	}

	for name, c := range map[string]struct {
		body map[string]any
		loc  string
	}{
		"unknown timezone": {settings(map[string]any{"timezone": "Mars/Olympus"}), "body.timezone"},
		"Local timezone":   {settings(map[string]any{"timezone": "Local"}), "body.timezone"},
		"retention zero":   {settings(map[string]any{"mediaRetentionDays": 0}), "body.mediaRetentionDays"},
		"bad visibility":   {settings(map[string]any{"siblingVisibility": "public"}), "body.siblingVisibility"},
		"bad day end":      {settings(map[string]any{"dayEnd": "25:00"}), "body.dayEnd"},
	} {
		rec := s.do("PUT", "/api/v1/household", c.body, mom)
		wantCode(t, name, rec, 422, "validation_failed")
		if !strings.Contains(rec.Body.String(), c.loc) {
			t.Errorf("%s: %s missing %s", name, rec.Body, c.loc)
		}
	}
}

func TestHouseholdDayEndRespectsKidOverrides(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	wantCode(t, "kid day end", s.do("PUT", "/api/v1/kids/sam", map[string]any{"displayName": "Sam", "color": "#3b82f6", "dayEndOverride": "20:00"}, mom), 200, "")

	rec := s.do("PUT", "/api/v1/household", settings(map[string]any{"dayEnd": "19:00"}), mom)
	wantCode(t, "day end before a kid's", rec, 422, "validation_failed")
	if !strings.Contains(rec.Body.String(), "Sam (20:00)") {
		t.Errorf("error doesn't name the kid: %s", rec.Body)
	}
	wantCode(t, "day end after the kid's", s.do("PUT", "/api/v1/household", settings(map[string]any{"dayEnd": "20:30"}), mom), 200, "")
	wantCode(t, "midnight is the latest", s.do("PUT", "/api/v1/household", settings(map[string]any{"dayEnd": "00:00"}), mom), 200, "")
}

func TestHouseholdCalendarChangeEmitsEvent(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	count := func() int {
		t.Helper()
		evs, err := s.store.ListUndispatchedEvents(t.Context(), 100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range evs {
			if e.Type == "household.calendar_changed" {
				n++
			}
		}
		return n
	}
	wantCode(t, "rename only", s.do("PUT", "/api/v1/household", settings(map[string]any{"name": "Bradys"}), mom), 200, "")
	if n := count(); n != 0 {
		t.Errorf("rename emitted %d calendar events", n)
	}
	wantCode(t, "timezone", s.do("PUT", "/api/v1/household", settings(map[string]any{"timezone": "Europe/Berlin"}), mom), 200, "")
	wantCode(t, "week start", s.do("PUT", "/api/v1/household", settings(map[string]any{"timezone": "Europe/Berlin", "weekStart": "monday"}), mom), 200, "")
	if n := count(); n != 2 {
		t.Errorf("calendar changes emitted %d events, want 2", n)
	}
}

func TestHouseholdPermissionsAndServerSettings(t *testing.T) {
	s := newAuthServerWith(t, config.Config{
		PublicURL: "https://chores.example.com", DataDir: "/data",
		OIDC:    config.OIDCConfig{Issuer: "https://id.example.com", ClientID: "chorecast", ClientSecret: "hunter2"},
		FromEnv: map[string]bool{"CHORECAST_PUBLIC_URL": true, "CHORECAST_OIDC_ISSUER": true, "CHORECAST_OIDC_CLIENT_ID": true, "CHORECAST_OIDC_CLIENT_SECRET": true},
	})
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	_, token := invite(t, s, mom, "parent")
	dad := sessionCookie(t, accept(s, token, "Dad", "dad", "dad password"))
	sam, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	device, _, _ := auth.NewStore(s.store.Queries, s.clk).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDevice, UserID: sam.ID})

	wantCode(t, "kid reads household", s.bearer("GET", "/api/v1/household", device, nil), 200, "")
	wantCode(t, "kid changes household", s.bearer("PUT", "/api/v1/household", device, settings(nil)), 403, "insufficient_scope")
	wantCode(t, "parent changes household", s.do("PUT", "/api/v1/household", settings(nil), dad), 200, "")
	wantCode(t, "parent reads server settings", s.do("GET", "/api/v1/server/settings", nil, dad), 403, "insufficient_scope")

	rec := s.do("GET", "/api/v1/server/settings", nil, mom)
	wantCode(t, "owner reads server settings", rec, 200, "")
	b := rec.Body.String()
	if strings.Contains(b, "hunter2") {
		t.Fatalf("server settings leaked the client secret: %s", b)
	}
	for _, want := range []string{
		`{"key":"publicUrl","env":"CHORECAST_PUBLIC_URL","value":"https://chores.example.com","source":"environment"}`,
		`{"key":"dataDir","env":"CHORECAST_DATA_DIR","value":"/data","source":"default"}`,
		`{"key":"oidcClientSecret","env":"CHORECAST_OIDC_CLIENT_SECRET","secret":true,"source":"environment"}`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("server settings missing %s in %s", want, b)
		}
	}
}
