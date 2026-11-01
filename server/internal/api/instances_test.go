package api_test

import (
	"fmt"
	"testing"

	"github.com/voidrot/chorecast/server/internal/horizon"
)

type instanceJSON struct {
	ID      int64
	ChoreID int64
	KidID   int64
	Date    string
	Status  string
	Planned bool
}

func TestListChoreInstances(t *testing.T) {
	s := newAuthServer(t) // Saturday 31 October 2026, 07:00 in Chicago
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	sam, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	sam2, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam-2"))
	wantCode(t, "create chore", s.do("POST", "/api/v1/chores", map[string]any{
		"name": "Dishes", "points": 1, "requiresApproval": false, "evidence": map[string]any{"photos": 0},
		"schedule": map[string]any{"type": "daily", "startDate": "2026-10-31"},
	}, mom), 201, "")
	if _, err := horizon.New(s.store, s.clk).Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	list := func(what, query string, as ...any) []instanceJSON {
		t.Helper()
		path := "/api/v1/instances" + query
		rec := s.do("GET", path, nil, mom)
		if len(as) > 0 {
			rec = s.bearer("GET", path, as[0].(string), nil)
		}
		wantCode(t, what, rec, 200, "")
		return decode[struct{ Items []instanceJSON }](t, rec).Items
	}

	all := list("parent, every kid", "")
	if len(all) != 16 {
		t.Fatalf("parent sees %d instances, want 2 kids x 8 days", len(all))
	}
	for _, i := range all {
		if i.Planned != (i.Date != "2026-10-31") {
			t.Errorf("%s planned = %v; days after today are planned", i.Date, i.Planned)
		}
	}
	if got := list("parent, one kid", fmt.Sprintf("?kidId=%d", sam.ID)); len(got) != 8 || got[0].KidID != sam.ID {
		t.Errorf("parent filtered to Sam: %+v", got)
	}
	if got := list("explicit range", "?from=2026-11-02"); len(got) != 2*6 || got[0].Date != "2026-11-02" {
		t.Errorf("from 2 Nov (to defaults to +7, the horizon ends 7 Nov): %d items", len(got))
	}

	_, token := pairDevice(t, s, pair(t, s, mom, "sam").Code)
	mine := list("kid device", "", token)
	if len(mine) != 8 {
		t.Errorf("kid sees %d instances, want their own 8", len(mine))
	}
	for _, i := range mine {
		if i.KidID != sam.ID {
			t.Errorf("kid sees a sibling's instance: %+v", i)
		}
	}
	wantCode(t, "kid asks for a sibling", s.bearer("GET", fmt.Sprintf("/api/v1/instances?kidId=%d", sam2.ID), token, nil), 403, "forbidden")

	for what, q := range map[string]string{
		"bad date": "?from=31-10-2026", "to before from": "?from=2026-11-02&to=2026-11-01",
		"too long": "?from=2026-01-01&to=2026-03-31",
	} {
		wantCode(t, what, s.do("GET", "/api/v1/instances"+q, nil, mom), 422, "validation_failed")
	}
	wantCode(t, "signed out", s.do("GET", "/api/v1/instances", nil), 401, "unauthenticated")
}
