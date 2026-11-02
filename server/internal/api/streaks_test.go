package api_test

import (
	"slices"
	"strings"
	"testing"
)

type streakMilestoneJSON struct{ Days, Points int }

type kidStreaksJSON struct {
	Daily struct {
		Current, Best int
		LastDay       string
	}
	Chores []struct{ ChoreID, Current, Best int64 }
}

// A kid reads their own streaks, not a sibling's; parents read any kid's.
func TestKidStreaksAccess(t *testing.T) {
	s := newAuthServerWith(t, configWithPublicURL())
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	sam, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	if _, err := s.store.SQL.ExecContext(t.Context(),
		`INSERT INTO kid_streaks (kid_id, current, best, last_day) VALUES (?, 3, 5, '2026-10-30')`, sam.ID); err != nil {
		t.Fatal(err)
	}
	_, samDevice := pairDevice(t, s, pair(t, s, mom, "sam").Code)

	rec := s.bearer("GET", "/api/v1/kids/sam/streaks", samDevice, nil)
	wantCode(t, "own streaks", rec, 200, "")
	if got := decode[kidStreaksJSON](t, rec); got.Daily.Current != 3 || got.Daily.Best != 5 || got.Daily.LastDay != "2026-10-30" || len(got.Chores) != 0 {
		t.Errorf("own streaks = %+v", got)
	}
	wantCode(t, "sibling streaks", s.bearer("GET", "/api/v1/kids/sam-2/streaks", samDevice, nil), 403, "forbidden")
	wantCode(t, "anonymous", s.do("GET", "/api/v1/kids/sam/streaks", nil), 401, "unauthenticated")

	rec = s.do("GET", "/api/v1/kids/sam-2/streaks", nil, mom)
	wantCode(t, "parent", rec, 200, "")
	if got := decode[kidStreaksJSON](t, rec); got.Daily.Current != 0 || got.Daily.LastDay != "" {
		t.Errorf("never evaluated = %s", rec.Body)
	}
}

// Streak milestones default to 3/7/30 days, are stored sorted, need distinct days, and are
// cleared when a PUT leaves them out.
func TestHouseholdStreakMilestones(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	type milestones struct{ StreakMilestones []streakMilestoneJSON }

	got := decode[milestones](t, s.do("GET", "/api/v1/household", nil, mom)).StreakMilestones
	if want := []streakMilestoneJSON{{3, 5}, {7, 15}, {30, 50}}; !slices.Equal(got, want) {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}

	rec := s.do("PUT", "/api/v1/household", settings(map[string]any{
		"streakMilestones": []map[string]int{{"days": 10, "points": 20}, {"days": 5, "points": 8}},
	}), mom)
	wantCode(t, "update", rec, 200, "")
	if got := decode[milestones](t, rec).StreakMilestones; !slices.Equal(got, []streakMilestoneJSON{{5, 8}, {10, 20}}) {
		t.Errorf("updated = %+v, want sorted by days", got)
	}

	if got := decode[milestones](t, s.do("PUT", "/api/v1/household", settings(nil), mom)).StreakMilestones; len(got) != 0 {
		t.Errorf("left out: %+v, want cleared", got)
	}
	var stored string
	if err := s.store.SQL.QueryRowContext(t.Context(), `SELECT streak_milestones FROM household`).Scan(&stored); err != nil || stored != "[]" {
		t.Errorf("stored %q, %v; want []", stored, err)
	}

	for name, c := range map[string]struct {
		milestones []map[string]int
		loc        string
	}{
		"same days":   {[]map[string]int{{"days": 7, "points": 1}, {"days": 7, "points": 2}}, "body.streakMilestones[1].days"},
		"zero days":   {[]map[string]int{{"days": 0, "points": 1}}, "body.streakMilestones[0].days"},
		"zero points": {[]map[string]int{{"days": 3, "points": 0}}, "body.streakMilestones[0].points"},
	} {
		rec := s.do("PUT", "/api/v1/household", settings(map[string]any{"streakMilestones": c.milestones}), mom)
		wantCode(t, name, rec, 422, "validation_failed")
		if !strings.Contains(rec.Body.String(), c.loc) {
			t.Errorf("%s: %s missing %s", name, rec.Body, c.loc)
		}
	}
}
