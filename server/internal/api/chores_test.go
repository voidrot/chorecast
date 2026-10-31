package api_test

import (
	"strings"
	"testing"

	"github.com/voidrot/chorecast/server/internal/auth"
)

type choreJSON struct {
	ID               int64
	Name             string
	Description      string
	Points, Penalty  int
	EstimatedMinutes *int
	MinAge           *int
	Steps            []string
	Slots            []string
	RequiresApproval bool
	Escalate         bool
	AutoApproveHours *int
	Evidence         struct {
		Photos                              int
		BeforeAfter, Video, Checklist, Note bool
	}
	LocationID   *int64
	EquipmentIDs []int64
	Eligibility  []struct {
		KidID int64
		Allow bool
	}
	Disabled bool
}

func TestChoreDefinitions(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	sam, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))

	loc := decode[struct{ ID int64 }](t, s.do("POST", "/api/v1/locations", map[string]string{"name": "Kitchen"}, mom)).ID
	vac := decode[struct{ ID int64 }](t, s.do("POST", "/api/v1/equipment", map[string]string{"name": "Vacuum", "description": "Hall closet"}, mom)).ID
	wantCode(t, "duplicate location", s.do("POST", "/api/v1/locations", map[string]string{"name": "Kitchen"}, mom), 409, "conflict")

	rec := s.do("POST", "/api/v1/chores", map[string]any{
		"name": " Feed the dog ", "description": "Half a scoop", "points": 5, "penalty": 2, "estimatedMinutes": 5, "minAge": 9,
		"steps": []string{" Scoop food ", "", "Fresh water"}, "slots": []string{"18:00", "07:00", "07:00"},
		"requiresApproval": true, "escalate": true, "autoApproveHours": 0,
		"evidence":   map[string]any{"photos": 1, "checklist": true},
		"locationId": loc, "equipmentIds": []int64{vac},
		"eligibility": []map[string]any{{"kidId": sam.ID, "allow": true}},
	}, mom)
	wantCode(t, "create", rec, 201, "")
	c := decode[choreJSON](t, rec)
	if c.Name != "Feed the dog" || strings.Join(c.Steps, "|") != "Scoop food|Fresh water" || strings.Join(c.Slots, ",") != "07:00,18:00" ||
		c.AutoApproveHours == nil || *c.AutoApproveHours != 0 || !c.Escalate || c.LocationID == nil || *c.LocationID != loc ||
		len(c.EquipmentIDs) != 1 || len(c.Eligibility) != 1 || !c.Eligibility[0].Allow || *c.MinAge != 9 {
		t.Errorf("created = %+v", c)
	}

	// A kid can read chores but not change them.
	device, _, _ := auth.NewStore(s.store.Queries, s.clk).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDevice, UserID: sam.ID})
	if rec := s.bearer("GET", "/api/v1/chores", device, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Feed the dog") {
		t.Errorf("kid lists chores = %d %s", rec.Code, rec.Body)
	}
	wantCode(t, "kid creates chore", s.bearer("POST", "/api/v1/chores", device, map[string]any{"name": "x", "points": 1, "requiresApproval": false, "evidence": map[string]any{"photos": 0}}), 403, "insufficient_scope")

	// Update replaces: optional fields left out are cleared; absent autoApproveHours = household default.
	rec = s.do("PUT", "/api/v1/chores/"+itoa(c.ID), map[string]any{
		"name": "Feed the dog", "points": 3, "requiresApproval": false, "evidence": map[string]any{"photos": 0}, "disabled": true,
	}, mom)
	wantCode(t, "update", rec, 200, "")
	u := decode[choreJSON](t, rec)
	if u.Points != 3 || u.AutoApproveHours != nil || u.LocationID != nil || len(u.EquipmentIDs) != 0 || len(u.Eligibility) != 0 ||
		len(u.Steps) != 0 || len(u.Slots) != 0 || u.MinAge != nil || !u.Disabled || u.Escalate {
		t.Errorf("updated = %+v", u)
	}

	for name, tc := range map[string]struct {
		body map[string]any
		loc  string
	}{
		"before/after with one photo": {map[string]any{"evidence": map[string]any{"photos": 1, "beforeAfter": true}}, "body.evidence.beforeAfter"},
		"checklist without steps":     {map[string]any{"evidence": map[string]any{"photos": 0, "checklist": true}}, "body.evidence.checklist"},
		"bad slot":                    {map[string]any{"slots": []string{"7pm"}}, "body.slots[0]"},
		"too many slots":              {map[string]any{"slots": []string{"01:00", "02:00", "03:00", "04:00", "05:00", "06:00", "07:00"}}, "body.slots"},
		"unknown location":            {map[string]any{"locationId": 999}, "body.locationId"},
		"unknown equipment":           {map[string]any{"equipmentIds": []int{999}}, "body.equipmentIds[0]"},
		"unknown kid":                 {map[string]any{"eligibility": []map[string]any{{"kidId": 999, "allow": false}}}, "body.eligibility[0].kidId"},
		"blank name":                  {map[string]any{"name": "  "}, "body.name"},
	} {
		body := map[string]any{"name": "Chore", "points": 1, "requiresApproval": true, "evidence": map[string]any{"photos": 0}}
		for k, v := range tc.body {
			body[k] = v
		}
		rec := s.do("POST", "/api/v1/chores", body, mom)
		wantCode(t, name, rec, 422, "validation_failed")
		if !strings.Contains(rec.Body.String(), tc.loc) {
			t.Errorf("%s: %s missing %s", name, rec.Body, tc.loc)
		}
	}

	// Deleting a catalog entry unlinks it from chores.
	rec = s.do("POST", "/api/v1/chores", map[string]any{"name": "Wipe table", "points": 2, "requiresApproval": false, "evidence": map[string]any{"photos": 0}, "locationId": loc}, mom)
	wipe := decode[choreJSON](t, rec)
	wantCode(t, "rename location", s.do("PUT", "/api/v1/locations/"+itoa(loc), map[string]string{"name": "Kitchen & dining"}, mom), 200, "")
	wantCode(t, "delete location", s.do("DELETE", "/api/v1/locations/"+itoa(loc), nil, mom), 204, "")
	if got := decode[choreJSON](t, s.do("GET", "/api/v1/chores/"+itoa(wipe.ID), nil, mom)); got.LocationID != nil {
		t.Errorf("location still linked: %+v", got)
	}
	wantCode(t, "delete unknown location", s.do("DELETE", "/api/v1/locations/"+itoa(loc), nil, mom), 404, "not_found")

	list := decode[struct{ Items []choreJSON }](t, s.do("GET", "/api/v1/chores", nil, mom)).Items
	if len(list) != 2 || list[0].Name != "Wipe table" { // enabled first
		t.Errorf("list order = %+v", list)
	}
	wantCode(t, "delete chore", s.do("DELETE", "/api/v1/chores/"+itoa(c.ID), nil, mom), 204, "")
	wantCode(t, "get deleted", s.do("GET", "/api/v1/chores/"+itoa(c.ID), nil, mom), 404, "not_found")
}
