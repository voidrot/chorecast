package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/horizon"
)

type starterItemJSON struct {
	Key, Name string
	Exists    bool
}

type starterPackJSON struct {
	Locations []starterItemJSON
	Equipment []starterItemJSON
	Chores    []struct {
		Key, Name        string
		Points           int
		MinAge           *int
		Assignment       string
		Location         string
		Equipment        []string
		Exists           bool
		RequiresApproval bool
	}
}

type starterCreatedJSON struct {
	Key, Name string
	ID        int64
}

type starterResultJSON struct {
	Created struct{ Locations, Equipment, Chores []starterCreatedJSON }
	Skipped []string
}

func (p starterPackJSON) keys() []string {
	keys := make([]string, 0, len(p.Chores))
	for _, c := range p.Chores {
		keys = append(keys, c.Key)
	}
	return keys
}

// starterEnv is a household with a parent session and the pack as the server serves it.
type starterEnv struct {
	*setupServer
	mom  *http.Cookie
	pack starterPackJSON
}

func newStarterEnv(t *testing.T) starterEnv {
	t.Helper()
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	rec := s.do("GET", "/api/v1/chores/starter-pack", nil, mom)
	wantCode(t, "get starter pack", rec, 200, "")
	return starterEnv{setupServer: s, mom: mom, pack: decode[starterPackJSON](t, rec)}
}

func (e starterEnv) apply(t *testing.T, keys []string, disabled bool) (*starterResultJSON, int) {
	t.Helper()
	rec := e.do("POST", "/api/v1/chores/starter-pack/apply", map[string]any{"chores": keys, "disabled": disabled}, e.mom)
	if rec.Code != 200 {
		return nil, rec.Code
	}
	r := decode[starterResultJSON](t, rec)
	return &r, rec.Code
}

func (e starterEnv) mustApply(t *testing.T, keys []string, disabled bool) starterResultJSON {
	t.Helper()
	r, code := e.apply(t, keys, disabled)
	if r == nil {
		t.Fatalf("apply %v = %d", keys, code)
	}
	return *r
}

func (e starterEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.store.SQL.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e starterEnv) rows(t *testing.T) (chores, locations, equipment, events int) {
	t.Helper()
	return e.count(t, "SELECT count(*) FROM chores"), e.count(t, "SELECT count(*) FROM locations"),
		e.count(t, "SELECT count(*) FROM equipment"), e.count(t, "SELECT count(*) FROM events WHERE type = 'chore.changed'")
}

// withRefs is a pack chore that uses a location and equipment.
func (e starterEnv) withRefs(t *testing.T) (key string, locName string, equipNames []string) {
	t.Helper()
	for _, c := range e.pack.Chores {
		if c.Location == "" || len(c.Equipment) == 0 {
			continue
		}
		name := func(items []starterItemJSON, k string) string {
			for _, it := range items {
				if it.Key == k {
					return it.Name
				}
			}
			t.Fatalf("no catalog key %q", k)
			return ""
		}
		for _, k := range c.Equipment {
			equipNames = append(equipNames, name(e.pack.Equipment, k))
		}
		return c.Key, name(e.pack.Locations, c.Location), equipNames
	}
	t.Fatal("no starter chore with a location and equipment")
	return "", "", nil
}

func TestStarterPackPreviewMarksExisting(t *testing.T) {
	e := newStarterEnv(t)
	for _, c := range e.pack.Chores {
		if c.Exists {
			t.Fatalf("fresh household: chore %s exists", c.Key)
		}
	}
	for _, it := range append(slices.Clone(e.pack.Locations), e.pack.Equipment...) {
		if it.Exists {
			t.Fatalf("fresh household: %s exists", it.Key)
		}
	}

	// A different case and stray spaces still match.
	first := e.pack.Locations[0]
	wantCode(t, "create location", e.do("POST", "/api/v1/locations", map[string]string{"name": "  " + strings.ToUpper(first.Name)}, e.mom), 201, "")
	got := decode[starterPackJSON](t, e.do("GET", "/api/v1/chores/starter-pack", nil, e.mom))
	for i, l := range got.Locations {
		if want := i == 0; l.Exists != want {
			t.Errorf("location %s exists = %v, want %v", l.Key, l.Exists, want)
		}
	}
}

func TestStarterPackApply(t *testing.T) {
	e := newStarterEnv(t)
	key, locName, equipNames := e.withRefs(t)
	var want struct {
		Points, MinAge int
		Assignment     string
		Approval       bool
	}
	for _, c := range e.pack.Chores {
		if c.Key == key {
			want.Points, want.Assignment, want.Approval = c.Points, c.Assignment, c.RequiresApproval
			if c.MinAge != nil {
				want.MinAge = *c.MinAge
			}
		}
	}

	r := e.mustApply(t, []string{key}, false)
	if len(r.Created.Chores) != 1 || r.Created.Chores[0].Key != key || len(r.Skipped) != 0 {
		t.Fatalf("result = %+v", r)
	}
	if len(r.Created.Locations) != 1 || r.Created.Locations[0].Name != locName || len(r.Created.Equipment) != len(equipNames) {
		t.Errorf("created catalog = %+v", r.Created)
	}

	c := decode[choreJSON](t, e.do("GET", "/api/v1/chores/"+itoa(r.Created.Chores[0].ID), nil, e.mom))
	if c.Schedule["startDate"] != "2026-10-31" || c.Assignment.Mode != want.Assignment || c.Points != want.Points ||
		c.RequiresApproval != want.Approval || c.Disabled || (want.MinAge != 0 && (c.MinAge == nil || *c.MinAge != want.MinAge)) {
		t.Errorf("chore = %+v", c)
	}
	if c.LocationID == nil || *c.LocationID != r.Created.Locations[0].ID {
		t.Errorf("locationId = %v, want %d", c.LocationID, r.Created.Locations[0].ID)
	}
	var wantEq []int64
	for _, q := range r.Created.Equipment {
		wantEq = append(wantEq, q.ID)
	}
	slices.Sort(wantEq)
	slices.Sort(c.EquipmentIDs)
	if !slices.Equal(c.EquipmentIDs, wantEq) {
		t.Errorf("equipmentIds = %v, want %v", c.EquipmentIDs, wantEq)
	}
}

func TestStarterPackReusesExistingCatalog(t *testing.T) {
	e := newStarterEnv(t)
	key, locName, equipNames := e.withRefs(t)
	loc := decode[struct{ ID int64 }](t, e.do("POST", "/api/v1/locations", map[string]string{"name": strings.ToUpper(locName)}, e.mom)).ID
	eq := decode[struct{ ID int64 }](t, e.do("POST", "/api/v1/equipment", map[string]string{"name": strings.ToLower(equipNames[0])}, e.mom)).ID

	r := e.mustApply(t, []string{key}, false)
	if len(r.Created.Locations) != 0 || len(r.Created.Chores) != 1 {
		t.Fatalf("result = %+v", r)
	}
	if len(r.Created.Equipment) != len(equipNames)-1 {
		t.Errorf("created equipment = %+v, want all but the existing one", r.Created.Equipment)
	}
	c := decode[choreJSON](t, e.do("GET", "/api/v1/chores/"+itoa(r.Created.Chores[0].ID), nil, e.mom))
	if c.LocationID == nil || *c.LocationID != loc || !slices.Contains(c.EquipmentIDs, eq) {
		t.Errorf("chore links = location %v equipment %v, want %d and %d", c.LocationID, c.EquipmentIDs, loc, eq)
	}
	if _, locs, _, _ := e.rows(t); locs != 1 {
		t.Errorf("locations = %d, want the one that existed", locs)
	}
}

func TestStarterPackApplyIsIdempotent(t *testing.T) {
	e := newStarterEnv(t)
	keys := e.pack.keys()

	first := e.mustApply(t, keys, false)
	if len(first.Created.Chores) != len(keys) || len(first.Skipped) != 0 {
		t.Fatalf("first apply = %+v", first)
	}
	chores, locs, eq, evs := e.rows(t)
	if chores != len(keys) || evs != 1 {
		t.Fatalf("after first apply: %d chores, %d chore.changed events", chores, evs)
	}

	second := e.mustApply(t, keys, false)
	if len(second.Created.Chores)+len(second.Created.Locations)+len(second.Created.Equipment) != 0 || !slices.Equal(second.Skipped, keys) {
		t.Errorf("second apply = %+v", second)
	}
	if c2, l2, e2, ev2 := e.rows(t); c2 != chores || l2 != locs || e2 != eq || ev2 != evs {
		t.Errorf("rows changed on the second apply: %d/%d/%d/%d -> %d/%d/%d/%d", chores, locs, eq, evs, c2, l2, e2, ev2)
	}
	if got := decode[struct{ Items []choreJSON }](t, e.do("GET", "/api/v1/chores", nil, e.mom)).Items; len(got) != len(keys) {
		t.Errorf("GET /chores = %d chores, want %d", len(got), len(keys))
	}
	for _, c := range decode[starterPackJSON](t, e.do("GET", "/api/v1/chores/starter-pack", nil, e.mom)).Chores {
		if !c.Exists {
			t.Errorf("chore %s not marked as existing after apply", c.Key)
		}
	}
}

func TestStarterPackSkipsExistingChoreName(t *testing.T) {
	e := newStarterEnv(t)
	key, _, _ := e.withRefs(t)
	var name string
	for _, c := range e.pack.Chores {
		if c.Key == key {
			name = c.Name
		}
	}
	// A paused chore with the name in another case still counts.
	wantCode(t, "create chore", e.do("POST", "/api/v1/chores", map[string]any{
		"name": strings.ToLower(name), "points": 1, "requiresApproval": false, "evidence": map[string]any{"photos": 0}, "disabled": true,
	}, e.mom), 201, "")

	r := e.mustApply(t, []string{key}, false)
	if !slices.Equal(r.Skipped, []string{key}) || len(r.Created.Chores)+len(r.Created.Locations)+len(r.Created.Equipment) != 0 {
		t.Errorf("result = %+v", r)
	}
	if chores, locs, eq, evs := e.rows(t); chores != 1 || locs != 0 || eq != 0 || evs != 1 { // evs: the create above
		t.Errorf("rows = %d chores, %d locations, %d equipment, %d events", chores, locs, eq, evs)
	}
}

func TestStarterPackUnknownKeyWritesNothing(t *testing.T) {
	e := newStarterEnv(t)
	key, _, _ := e.withRefs(t)

	rec := e.do("POST", "/api/v1/chores/starter-pack/apply", map[string]any{"chores": []string{key, "nope"}}, e.mom)
	wantCode(t, "unknown key", rec, 422, "validation_failed")
	if !strings.Contains(rec.Body.String(), "body.chores[1]") {
		t.Errorf("body = %s, want the unknown key's location", rec.Body)
	}
	if chores, locs, eq, evs := e.rows(t); chores+locs+eq+evs != 0 {
		t.Errorf("rows after a refused apply = %d chores, %d locations, %d equipment, %d events", chores, locs, eq, evs)
	}
	wantCode(t, "duplicate keys", e.do("POST", "/api/v1/chores/starter-pack/apply", map[string]any{"chores": []string{key, key}}, e.mom), 422, "")
	wantCode(t, "no keys", e.do("POST", "/api/v1/chores/starter-pack/apply", map[string]any{"chores": []string{}}, e.mom), 422, "")
}

// A failure part-way through undoes the whole apply: no chore, catalog entry or event stays.
func TestStarterPackApplyRollsBack(t *testing.T) {
	e := newStarterEnv(t)
	keys := e.pack.keys()
	// The second chore insert fails, after the first chore and its catalog entries were written.
	if _, err := e.store.SQL.ExecContext(t.Context(), `CREATE TRIGGER fail_second BEFORE INSERT ON chores
		WHEN (SELECT count(*) FROM chores) >= 1 BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}

	if _, code := e.apply(t, keys, false); code != 500 {
		t.Fatalf("apply with a failing insert = %d, want 500", code)
	}
	if chores, locs, eq, evs := e.rows(t); chores+locs+eq+evs != 0 {
		t.Errorf("rows after a failed apply = %d chores, %d locations, %d equipment, %d events", chores, locs, eq, evs)
	}
}

func TestStarterPackDisabledAndHorizon(t *testing.T) {
	instances := func(e starterEnv, r starterResultJSON) int {
		t.Helper()
		if _, err := horizon.New(e.store, e.clk).Generate(t.Context()); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, c := range r.Created.Chores {
			n += e.count(t, "SELECT count(*) FROM chore_instances WHERE chore_id = ?", c.ID)
		}
		return n
	}

	paused := newStarterEnv(t)
	r := paused.mustApply(t, paused.pack.keys(), true)
	for _, c := range r.Created.Chores {
		if got := decode[choreJSON](t, paused.do("GET", "/api/v1/chores/"+itoa(c.ID), nil, paused.mom)); !got.Disabled {
			t.Errorf("chore %s is not paused", c.Key)
		}
	}
	if n := instances(paused, r); n != 0 {
		t.Errorf("paused chores have %d instances", n)
	}

	active := newStarterEnv(t)
	r = active.mustApply(t, active.pack.keys(), false)
	if n := instances(active, r); n == 0 {
		t.Error("enabled chores have no instances after the horizon runs")
	}
}

func TestStarterPackScopes(t *testing.T) {
	e := newStarterEnv(t)
	key, _, _ := e.withRefs(t)
	sam, _ := e.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	device, _, err := auth.NewStore(e.store.Queries, e.clk).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDevice, UserID: sam.ID})
	if err != nil {
		t.Fatal(err)
	}

	wantCode(t, "kid reads the pack", e.bearer("GET", "/api/v1/chores/starter-pack", device, nil), 403, "insufficient_scope")
	wantCode(t, "kid applies the pack", e.bearer("POST", "/api/v1/chores/starter-pack/apply", device, map[string]any{"chores": []string{key}}), 403, "insufficient_scope")
	if chores, locs, eq, evs := e.rows(t); chores+locs+eq+evs != 0 {
		t.Errorf("a refused apply wrote %d chores, %d locations, %d equipment, %d events", chores, locs, eq, evs)
	}
}
