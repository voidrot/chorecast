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

func TestDeleteChoreWithHistoryIsRefused(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	create := func(name string) int64 {
		rec := s.do("POST", "/api/v1/chores", map[string]any{
			"name": name, "points": 1, "requiresApproval": false, "evidence": map[string]any{"photos": 0},
			"schedule": map[string]any{"type": "daily", "startDate": "2026-10-31"},
		}, mom)
		wantCode(t, "create "+name, rec, 201, "")
		return decode[struct{ ID int64 }](t, rec).ID
	}
	done, upcoming := create("Dishes"), create("Laundry")
	if _, err := horizon.New(s.store, s.clk).Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SQL.ExecContext(t.Context(),
		"UPDATE chore_instances SET status = 'approved' WHERE id = (SELECT MIN(id) FROM chore_instances WHERE chore_id = ?)", done); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "delete a chore with history", s.do("DELETE", fmt.Sprintf("/api/v1/chores/%d", done), nil, mom), 409, "chore_has_history")
	wantCode(t, "delete a chore without history", s.do("DELETE", fmt.Sprintf("/api/v1/chores/%d", upcoming), nil, mom), 204, "")
}

type transitionJSON struct {
	ID                 int64
	KidID              int64
	Date               string
	Status             string
	ChoreName          string
	CompletedBy        int64
	RejectionCount     int
	LastRejection      *struct{ Reason, Text string }
	ManuallyReassigned bool
}

func TestChoreInstanceTransitions(t *testing.T) {
	s := newAuthServer(t) // Saturday 31 October 2026, 07:00 in Chicago
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	sam, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	sam2, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam-2"))
	chore := func(name string, assignment map[string]any) {
		wantCode(t, "create "+name, s.do("POST", "/api/v1/chores", map[string]any{
			"name": name, "points": 4, "requiresApproval": true, "evidence": map[string]any{"photos": 0},
			"schedule": map[string]any{"type": "daily", "startDate": "2026-10-31"}, "assignment": assignment,
		}, mom), 201, "")
	}
	chore("Dishes", map[string]any{"mode": "fixed", "kidIds": []int64{sam.ID, sam2.ID}})
	chore("Tidy", map[string]any{"mode": "shared", "kidIds": []int64{sam.ID, sam2.ID}, "sharedPoints": "split_evenly"})
	chore("Trash", map[string]any{"mode": "fixed", "kidIds": []int64{sam.ID}})
	if _, err := horizon.New(s.store, s.clk).Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	find := func(name, date string, kid int64) int64 {
		t.Helper()
		rec := s.do("GET", "/api/v1/instances", nil, mom)
		for _, i := range decode[struct{ Items []transitionJSON }](t, rec).Items {
			if i.ChoreName == name && i.Date == date && i.KidID == kid {
				return i.ID
			}
		}
		t.Fatalf("no %s instance on %s for kid %d", name, date, kid)
		return 0
	}
	const today, tomorrow = "2026-10-31", "2026-11-01"
	samDishes, sam2Dishes, tidy := find("Dishes", today, sam.ID), find("Dishes", today, sam2.ID), find("Tidy", today, 0)
	path := func(id int64, action string) string { return fmt.Sprintf("/api/v1/instances/%d/%s", id, action) }
	balance := func(kid string) int64 {
		t.Helper()
		rec := s.do("GET", "/api/v1/kids/"+kid+"/points", nil, mom)
		wantCode(t, "balance", rec, 200, "")
		return decode[struct{ Balance int64 }](t, rec).Balance
	}
	_, kid := pairDevice(t, s, pair(t, s, mom, "sam").Code)
	_, tablet := pairDevice(t, s, pair(t, s, mom, "").Code)

	// Who may call what.
	wantCode(t, "signed out", s.do("POST", path(samDishes, "completion"), nil), 401, "unauthenticated")
	wantCode(t, "shared device completes", s.bearer("POST", path(samDishes, "completion"), tablet, nil), 403, "insufficient_scope")
	for _, action := range []string{"approval", "rejection", "approval/reversal", "excusal", "reassignment"} {
		wantCode(t, "kid calls "+action, s.bearer("POST", path(samDishes, action), kid,
			map[string]any{"kidId": sam2.ID, "reason": "not_done"}), 403, "insufficient_scope")
	}
	wantCode(t, "kid completes a sibling's", s.bearer("POST", path(sam2Dishes, "completion"), kid, nil), 403, "forbidden")
	wantCode(t, "kid completes as a sibling", s.bearer("POST", path(samDishes, "completion"), kid, map[string]any{"kidId": sam2.ID}), 403, "forbidden")
	wantCode(t, "unknown instance", s.do("POST", path(99999, "approval"), nil, mom), 404, "instance_not_found")
	wantCode(t, "kid, unknown instance", s.bearer("POST", path(99999, "completion"), kid, nil), 404, "instance_not_found")

	// Complete and approve.
	rec := s.bearer("POST", path(samDishes, "completion"), kid, nil)
	wantCode(t, "kid completes their own", rec, 200, "")
	if got := decode[transitionJSON](t, rec); got.Status != "submitted" || got.CompletedBy != sam.ID {
		t.Errorf("completed: %+v", got)
	}
	wantCode(t, "complete twice", s.bearer("POST", path(samDishes, "completion"), kid, nil), 409, "instance_not_open")
	wantCode(t, "parent completes shared without naming the kid", s.do("POST", path(tidy, "completion"), nil, mom), 422, "validation_failed")
	wantCode(t, "kid completes a shared one they're in", s.bearer("POST", path(tidy, "completion"), kid, nil), 200, "")
	wantCode(t, "approve", s.do("POST", path(samDishes, "approval"), nil, mom), 200, "")
	wantCode(t, "approve shared", s.do("POST", path(tidy, "approval"), nil, mom), 200, "")
	if a, b := balance("sam"), balance("sam-2"); a != 4+2 || b != 2 {
		t.Errorf("balances %d and %d, want 6 (dishes + half of tidy) and 2", a, b)
	}
	wantCode(t, "approve again", s.do("POST", path(samDishes, "approval"), nil, mom), 409, "instance_not_submitted")

	// Reverse, reject, plan, reassign, excuse.
	rec = s.do("POST", path(samDishes, "approval/reversal"), map[string]any{"note": "Not done after all"}, mom)
	wantCode(t, "reverse the approval", rec, 200, "")
	if got := decode[transitionJSON](t, rec); got.Status != "open" || balance("sam") != 2 {
		t.Errorf("reversed: %+v, balance %d", got, balance("sam"))
	}
	wantCode(t, "parent completes for a kid", s.do("POST", path(sam2Dishes, "completion"), nil, mom), 200, "")
	wantCode(t, "reject without a reason", s.do("POST", path(sam2Dishes, "rejection"), map[string]any{}, mom), 422, "validation_failed")
	rec = s.do("POST", path(sam2Dishes, "rejection"), map[string]any{"reason": "not_done", "text": "The pots"}, mom)
	wantCode(t, "reject", rec, 200, "")
	if got := decode[transitionJSON](t, rec); got.Status != "open" || got.RejectionCount != 1 || got.LastRejection == nil ||
		got.LastRejection.Reason != "not_done" || got.LastRejection.Text != "The pots" {
		t.Errorf("rejected: %+v", got)
	}
	tidyTomorrow := find("Tidy", tomorrow, 0)
	wantCode(t, "complete tomorrow's", s.do("POST", path(tidyTomorrow, "completion"), map[string]any{"kidId": sam.ID}, mom), 409, "instance_planned")
	wantCode(t, "reassign a shared one", s.do("POST", path(tidyTomorrow, "reassignment"), map[string]any{"kidId": sam.ID}, mom), 409, "instance_shared")
	wantCode(t, "reassign onto a kid who has it",
		s.do("POST", path(find("Dishes", tomorrow, sam.ID), "reassignment"), map[string]any{"kidId": sam2.ID}, mom), 409, "instance_taken")
	rec = s.do("POST", path(find("Trash", tomorrow, sam.ID), "reassignment"), map[string]any{"kidId": sam2.ID}, mom)
	wantCode(t, "reassign", rec, 200, "")
	if got := decode[transitionJSON](t, rec); got.KidID != sam2.ID || !got.ManuallyReassigned {
		t.Errorf("reassigned: %+v", got)
	}
	trash := find("Trash", today, sam.ID)
	if _, err := s.store.SQL.ExecContext(t.Context(), "UPDATE chore_instances SET status = 'missed' WHERE id = ?", trash); err != nil {
		t.Fatal(err)
	}
	rec = s.do("POST", path(trash, "excusal"), nil, mom)
	wantCode(t, "excuse", rec, 200, "")
	if got := decode[transitionJSON](t, rec); got.Status != "excused" {
		t.Errorf("excused: %+v", got)
	}
	wantCode(t, "excuse again", s.do("POST", path(trash, "excusal"), nil, mom), 409, "instance_not_missed")
}
