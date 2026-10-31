package api_test

import (
	"strings"
	"testing"

	"github.com/voidrot/chorecast/server/internal/auth"
)

type kidJSON struct {
	ID             int64
	Slug           string
	DisplayName    string
	Color          string
	Avatar         string
	BirthDate      string
	PINRequired    bool
	PINSet         bool `json:"pinSet"`
	DayEndOverride string
	QuietHours     *struct{ Start, End string }
	SchoolHours    *struct {
		Days       []string
		Start, End string
	}
	ArchivedAt *string
}

func TestKidProfiles(t *testing.T) {
	s := newAuthServer(t) // household day end 21:00; kids sam and sam-2 from setup
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))

	list := decode[struct{ Items []kidJSON }](t, s.do("GET", "/api/v1/kids", nil, mom)).Items
	if len(list) != 2 || list[0].Avatar == "" || list[0].Avatar == list[1].Avatar {
		t.Fatalf("setup kids = %+v", list)
	}

	// Create, with validation.
	base := map[string]any{"displayName": "Alex", "color": "#A855F7", "avatar": "pixel-07", "birthDate": "2019-05-01"}
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for name, c := range map[string]struct {
		body map[string]any
		loc  string
	}{
		"unknown avatar":      {with(map[string]any{"avatar": "cat"}), "body.avatar"},
		"future birth date":   {with(map[string]any{"birthDate": "2099-01-01"}), "body.birthDate"},
		"day end after house": {with(map[string]any{"dayEndOverride": "22:00"}), "body.dayEndOverride"},
		"empty quiet window":  {with(map[string]any{"quietHours": map[string]string{"start": "20:00", "end": "20:00"}}), "body.quietHours"},
		"school ends early":   {with(map[string]any{"schoolHours": map[string]any{"days": []string{"monday"}, "start": "15:00", "end": "08:00"}}), "body.schoolHours"},
		"unknown school day":  {with(map[string]any{"schoolHours": map[string]any{"days": []string{"funday"}, "start": "08:00", "end": "15:00"}}), "body.schoolHours.days"},
		"bad slug":            {with(map[string]any{"slug": "12"}), "body.slug"},
	} {
		rec := s.do("POST", "/api/v1/kids", c.body, mom)
		wantCode(t, name, rec, 422, "validation_failed")
		if !strings.Contains(rec.Body.String(), c.loc) {
			t.Errorf("%s: body %s missing %s", name, rec.Body, c.loc)
		}
	}
	wantCode(t, "taken slug", s.do("POST", "/api/v1/kids", with(map[string]any{"slug": "sam"}), mom), 409, "conflict")

	rec := s.do("POST", "/api/v1/kids", with(map[string]any{
		"pinRequired": true, "dayEndOverride": "19:30",
		"quietHours":  map[string]string{"start": "20:00", "end": "07:00"},
		"schoolHours": map[string]any{"days": []string{"friday", "monday"}, "start": "08:00", "end": "15:00"},
	}), mom)
	wantCode(t, "create", rec, 201, "")
	alex := decode[kidJSON](t, rec)
	if alex.Slug != "alex" || alex.Color != "#a855f7" || alex.DayEndOverride != "19:30" || alex.QuietHours == nil ||
		alex.SchoolHours == nil || strings.Join(alex.SchoolHours.Days, ",") != "monday,friday" || !alex.PINRequired || alex.PINSet {
		t.Errorf("created = %+v", alex)
	}

	// Update: renaming keeps the slug; omitted optional fields are cleared.
	rec = s.do("PUT", "/api/v1/kids/alex", map[string]any{"displayName": "Alexandra", "color": "#22c55e"}, mom)
	wantCode(t, "update", rec, 200, "")
	got := decode[kidJSON](t, rec)
	if got.Slug != "alex" || got.DisplayName != "Alexandra" || got.Avatar != "" || got.BirthDate != "" || got.QuietHours != nil || got.SchoolHours != nil || got.DayEndOverride != "" {
		t.Errorf("updated = %+v", got)
	}
	rec = s.do("PUT", "/api/v1/kids/"+itoa(alex.ID), map[string]any{"displayName": "Alexandra", "color": "#22c55e", "slug": "lexi"}, mom)
	if wantCode(t, "change slug", rec, 200, ""); decode[kidJSON](t, rec).Slug != "lexi" {
		t.Errorf("slug change = %s", rec.Body)
	}
	wantCode(t, "old slug gone", s.do("GET", "/api/v1/kids/alex", nil, mom), 404, "kid_not_found")
	wantCode(t, "update unknown", s.do("PUT", "/api/v1/kids/nobody", base, mom), 404, "kid_not_found")

	// PIN.
	wantCode(t, "bad pin", s.do("PUT", "/api/v1/kids/lexi/pin", map[string]string{"pin": "12"}, mom), 422, "validation_failed")
	wantCode(t, "set pin", s.do("PUT", "/api/v1/kids/lexi/pin", map[string]string{"pin": "2468"}, mom), 204, "")
	u, _ := s.store.GetUser(t.Context(), alex.ID)
	if ok, _, _ := auth.VerifySecret("2468", u.PinHash.String); !ok {
		t.Error("kid PIN not stored")
	}
	if !decode[kidJSON](t, s.do("GET", "/api/v1/kids/lexi", nil, mom)).PINSet {
		t.Error("pinSet false after setting")
	}
	wantCode(t, "clear pin", s.do("PUT", "/api/v1/kids/lexi/pin", map[string]string{"pin": ""}, mom), 204, "")
}

func TestArchiveKid(t *testing.T) {
	s := newAuthServer(t)
	mom := sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	sam, err := s.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewStore(s.store.Queries, s.clk)
	samDevice, _, _ := tokens.Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDevice, UserID: sam.ID})
	sibling, _ := s.store.GetKidBySlug(t.Context(), sqlNull("sam-2"))
	siblingDevice, _, _ := tokens.Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDevice, UserID: sibling.ID})

	// Kids can see profiles but not change them, nor see archived kids.
	wantCode(t, "kid lists kids", s.bearer("GET", "/api/v1/kids", samDevice, nil), 200, "")
	wantCode(t, "kid creates kid", s.bearer("POST", "/api/v1/kids", samDevice, map[string]any{"displayName": "X", "color": "#000000"}), 403, "insufficient_scope")
	wantCode(t, "kid lists archived", s.bearer("GET", "/api/v1/kids?archived=true", samDevice, nil), 403, "insufficient_scope")

	rec := s.do("POST", "/api/v1/kids/sam/archive", nil, mom)
	wantCode(t, "archive", rec, 200, "")
	if decode[kidJSON](t, rec).ArchivedAt == nil {
		t.Error("archivedAt not set")
	}
	wantCode(t, "archived kid's device revoked", s.bearer("GET", "/api/v1/me", samDevice, nil), 401, "unauthenticated")
	wantCode(t, "sibling's device unaffected", s.bearer("GET", "/api/v1/me", siblingDevice, nil), 200, "")
	if strings.Contains(s.do("GET", "/api/v1/kids", nil, mom).Body.String(), `"slug":"sam"`) {
		t.Error("archived kid in default list")
	}
	all := decode[struct{ Items []kidJSON }](t, s.do("GET", "/api/v1/kids?archived=true", nil, mom)).Items
	if len(all) != 2 || all[len(all)-1].Slug != "sam" {
		t.Errorf("list with archived = %+v", all)
	}
	wantCode(t, "sibling can't see archived kid", s.bearer("GET", "/api/v1/kids/sam", siblingDevice, nil), 404, "kid_not_found")
	wantCode(t, "archive twice is fine", s.do("POST", "/api/v1/kids/sam/archive", nil, mom), 200, "")

	rec = s.do("POST", "/api/v1/kids/sam/unarchive", nil, mom)
	wantCode(t, "unarchive", rec, 200, "")
	if decode[kidJSON](t, rec).ArchivedAt != nil {
		t.Error("still archived")
	}
	wantCode(t, "devices stay revoked", s.bearer("GET", "/api/v1/me", samDevice, nil), 401, "unauthenticated")
}
