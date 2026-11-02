package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/parents"
)

type auditEntryJSON struct {
	ID         int64
	ActorKind  string
	ActorID    int64
	ActorName  string
	Action     string
	TargetKind string
	TargetID   int64
	Summary    string
	Details    audit.AuditDetails
}

type auditPageJSON struct {
	Items      []auditEntryJSON
	NextCursor string
}

// auditRows is every row in the table, oldest first, raw: summary and data as stored.
func auditRows(t *testing.T, s *setupServer) []struct{ ActorKind, Action, Summary, Data string } {
	t.Helper()
	rs, err := s.store.SQL.QueryContext(t.Context(), `SELECT actor_kind, action, summary, data FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []struct{ ActorKind, Action, Summary, Data string }
	for rs.Next() {
		var r struct{ ActorKind, Action, Summary, Data string }
		if err := rs.Scan(&r.ActorKind, &r.Action, &r.Summary, &r.Data); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func auditPage(t *testing.T, s *setupServer, as *http.Cookie, query string) auditPageJSON {
	t.Helper()
	rec := s.do("GET", "/api/v1/audit"+query, nil, as)
	wantCode(t, "audit log "+query, rec, 200, "")
	return decode[auditPageJSON](t, rec)
}

// Each audited action done through the API writes exactly one row naming the parent who did it,
// and a request that changes nothing writes none. No credential handed out along the way (invite
// token, temporary password, pairing code, device token) or typed in (passwords) is stored.
func TestAuditedActions(t *testing.T) {
	s, mom, dad, dadID := newHousehold(t)
	momID := decode[struct{ UserID int64 }](t, s.do("GET", "/api/v1/me", nil, mom)).UserID
	secrets := []string{ownerPassword, "dad password", "$argon2"}

	// newHousehold invited Dad, who accepted: he is the actor of his own joining.
	log := auditPage(t, s, mom, "")
	if len(log.Items) != 2 {
		t.Fatalf("after joining: %+v", log.Items)
	}
	if e := log.Items[1]; e.Action != audit.ActionInviteCreated || e.ActorID != momID || e.ActorName != "Mom" || e.Details.Role != "parent" {
		t.Errorf("invite created = %+v", e)
	}
	if e := log.Items[0]; e.Action != audit.ActionInviteAccepted || e.ActorID != dadID || e.TargetKind != audit.TargetParent ||
		e.TargetID != dadID || e.Summary != "Dad" || e.Details.Role != "parent" {
		t.Errorf("invite accepted = %+v", e)
	}

	// step runs one request and checks it added exactly one row: action by actor on target.
	step := func(what, action string, actor, target int64, do func()) auditEntryJSON {
		t.Helper()
		before := len(auditRows(t, s))
		do()
		if n := len(auditRows(t, s)); n != before+1 {
			t.Fatalf("%s: %d new rows, want 1", what, n-before)
		}
		e := auditPage(t, s, mom, "?limit=1").Items[0]
		if e.Action != action || e.ActorKind != audit.ActorParent || e.ActorID != actor || (target != 0 && e.TargetID != target) {
			t.Fatalf("%s: %+v, want %s by %d on %d", what, e, action, actor, target)
		}
		return e
	}
	none := func(what string, do func()) {
		t.Helper()
		before := len(auditRows(t, s))
		do()
		if n := len(auditRows(t, s)); n != before {
			t.Fatalf("%s: %d new rows, want none", what, n-before)
		}
	}

	e := step("settings", audit.ActionSettingsUpdated, dadID, 0, func() {
		wantCode(t, "update", s.do("PUT", "/api/v1/household", settings(map[string]any{"name": "Brady Bunch", "dayEnd": "20:30"}), dad), 200, "")
	})
	// The PUT leaves out the streak milestones, which clears them; structured values are JSON.
	want := []audit.AuditChange{
		{Field: "dayEnd", From: "21:00", To: "20:30"}, {Field: "name", From: "The Bradys", To: "Brady Bunch"},
		{Field: "streakMilestones", From: `[{"days":3,"points":5},{"days":7,"points":15},{"days":30,"points":50}]`, To: ""},
	}
	if e.TargetKind != audit.TargetHousehold || e.Summary != "Brady Bunch" || !slices.Equal(e.Details.Changes, want) {
		t.Errorf("settings = %+v", e)
	}
	none("same settings again", func() {
		wantCode(t, "update", s.do("PUT", "/api/v1/household", settings(map[string]any{"name": "Brady Bunch", "dayEnd": "20:30"}), mom), 200, "")
	})

	var inviteID int64
	var inviteToken string
	step("invite", audit.ActionInviteCreated, momID, 0, func() { inviteID, inviteToken = invite(t, s, mom, "owner") })
	secrets = append(secrets, inviteToken)
	step("revoke invite", audit.ActionInviteRevoked, momID, inviteID, func() {
		wantCode(t, "revoke", s.do("DELETE", "/api/v1/invites/"+itoa(inviteID), nil, mom), 204, "")
	})

	role := "/api/v1/parents/" + itoa(dadID) + "/role"
	e = step("promote", audit.ActionParentRoleChanged, momID, dadID, func() {
		wantCode(t, "promote", s.do("PUT", role, map[string]any{"owner": true}, mom), 200, "")
	})
	if e.Details.Role != "owner" || e.Summary != "Dad" {
		t.Errorf("promote = %+v", e)
	}
	none("promote again", func() { wantCode(t, "promote", s.do("PUT", role, map[string]any{"owner": true}, mom), 200, "") })
	step("demote", audit.ActionParentRoleChanged, momID, dadID, func() {
		wantCode(t, "demote", s.do("PUT", role, map[string]any{"owner": false}, mom), 200, "")
	})

	step("reset password", audit.ActionParentPasswordReset, momID, dadID, func() {
		rec := s.do("POST", "/api/v1/parents/"+itoa(dadID)+"/reset-password", nil, mom)
		wantCode(t, "reset", rec, 200, "")
		secrets = append(secrets, decode[struct{ Password string }](t, rec).Password)
	})
	step("disable 2FA", audit.ActionParentTOTPDisabled, momID, dadID, func() {
		wantCode(t, "disable 2fa", s.do("POST", "/api/v1/parents/"+itoa(dadID)+"/disable-totp", nil, mom), 204, "")
	})

	// Pairing is recorded when the device pairs, as the parent who made the code.
	p := pair(t, s, mom, "sam")
	var deviceID int64
	e = step("pair", audit.ActionDevicePaired, momID, 0, func() {
		var token string
		deviceID, token = pairDevice(t, s, p.Code)
		secrets = append(secrets, p.Code, strings.ReplaceAll(p.Code, "-", ""), token)
	})
	if e.TargetID != deviceID || e.Summary != "Pixel Tablet" || e.Details.KidID != p.KidID {
		t.Errorf("paired = %+v", e)
	}
	step("revoke device", audit.ActionDeviceRevoked, momID, deviceID, func() {
		wantCode(t, "revoke", s.do("DELETE", "/api/v1/devices/"+itoa(deviceID), nil, mom), 204, "")
	})

	e = step("archive", audit.ActionKidArchived, momID, p.KidID, func() {
		wantCode(t, "archive", s.do("POST", "/api/v1/kids/sam/archive", nil, mom), 200, "")
	})
	if e.Summary != "Sam" || e.TargetKind != audit.TargetKid {
		t.Errorf("archived = %+v", e)
	}
	none("archive again", func() { wantCode(t, "archive", s.do("POST", "/api/v1/kids/sam/archive", nil, mom), 200, "") })
	step("unarchive", audit.ActionKidUnarchived, momID, p.KidID, func() {
		wantCode(t, "unarchive", s.do("POST", "/api/v1/kids/sam/unarchive", nil, mom), 200, "")
	})

	// An adjustment is recorded from its event (the recorder, not running here); a manual
	// reversal in its own transaction.
	var entry int64
	none("adjust", func() {
		rec := s.do("POST", "/api/v1/kids/sam/points/adjustments", map[string]any{"amount": 5, "note": "allowance"}, mom)
		wantCode(t, "adjust", rec, 201, "")
		entry = decode[struct{ ID int64 }](t, rec).ID
	})
	e = step("reverse", audit.ActionPointsReversed, momID, 0, func() {
		wantCode(t, "reverse", s.do("POST", fmt.Sprintf("/api/v1/kids/sam/points/entries/%d/reversal", entry), map[string]any{"note": "oops"}, mom), 201, "")
	})
	if e.Details.ReversesID != entry || e.Details.Amount == nil || *e.Details.Amount != -5 || e.Details.KidID != p.KidID {
		t.Errorf("reversal = %+v", e)
	}

	step("remove", audit.ActionParentRemoved, momID, dadID, func() {
		wantCode(t, "remove", s.do("DELETE", "/api/v1/parents/"+itoa(dadID), nil, mom), 204, "")
	})

	// Removed parents are still named.
	all := auditPage(t, s, mom, "?limit=100")
	for _, e := range all.Items {
		if e.ActorID == dadID && e.ActorName != "Dad" {
			t.Errorf("removed parent's entry = %+v", e)
		}
	}
	body := s.do("GET", "/api/v1/audit?limit=100", nil, mom).Body.String()
	for _, r := range auditRows(t, s) {
		body += r.Summary + r.Data
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(body, secret) {
			t.Errorf("the audit log holds a secret %q", secret)
		}
	}
}

// Only owners read the audit log; filters combine and pages follow the cursor.
func TestAuditLogAccessFiltersAndPaging(t *testing.T) {
	s, mom, dad, dadID := newHousehold(t) // 2026-10-31: invite_created (Mom), invite_accepted (Dad)
	momID := decode[struct{ UserID int64 }](t, s.do("GET", "/api/v1/me", nil, mom)).UserID
	wantCode(t, "parent reads audit", s.do("GET", "/api/v1/audit", nil, dad), 403, "insufficient_scope")

	s.clk.Advance(24 * time.Hour) // 2026-11-01 in Chicago
	wantCode(t, "update", s.do("PUT", "/api/v1/household", settings(map[string]any{"name": "Brady Bunch"}), dad), 200, "")
	s.clk.Advance(24 * time.Hour) // 2026-11-02
	if _, err := parents.New(s.store, s.clk).ResetPassword(t.Context(), audit.CLI, dadID); err != nil {
		t.Fatal(err)
	}
	if err := audit.Record(t.Context(), s.store.Queries, s.clk.Now(), audit.Entry{
		Actor: audit.System, Action: audit.ActionChoreApproved, TargetKind: audit.TargetInstance, TargetID: 99,
	}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"", []string{"chore_approved", "parent_password_reset", "settings_updated", "invite_accepted", "invite_created"}},
		{"?actorKind=parent", []string{"settings_updated", "invite_accepted", "invite_created"}},
		{"?actorKind=parent&actorId=" + itoa(dadID), []string{"settings_updated", "invite_accepted"}},
		{"?actorId=" + itoa(momID), []string{"invite_created"}},
		{"?actorKind=cli", []string{"parent_password_reset"}},
		{"?actorKind=system", []string{"chore_approved"}},
		{"?action=settings_updated", []string{"settings_updated"}},
		{"?action=settings_updated&actorKind=cli", []string{}},
		{"?from=2026-11-01&to=2026-11-01", []string{"settings_updated"}},
		{"?from=2026-11-01", []string{"chore_approved", "parent_password_reset", "settings_updated"}},
		{"?to=2026-10-31", []string{"invite_accepted", "invite_created"}},
	} {
		got := []string{}
		for _, e := range auditPage(t, s, mom, c.query).Items {
			got = append(got, e.Action)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q = %v, want %v", c.query, got, c.want)
		}
	}
	if e := auditPage(t, s, mom, "?actorKind=cli").Items[0]; e.ActorID != 0 || e.ActorName != "" || e.TargetID != dadID || e.Summary != "Dad" {
		t.Errorf("cli entry = %+v", e)
	}

	for _, q := range []string{"?actorKind=kid", "?action=nope", "?from=yesterday", "?from=2026-11-02&to=2026-11-01"} {
		wantCode(t, q, s.do("GET", "/api/v1/audit"+q, nil, mom), 422, "validation_failed")
	}
	wantCode(t, "bad cursor", s.do("GET", "/api/v1/audit?cursor=nope", nil, mom), 400, "invalid_cursor")

	// Paging: newest first, every row once, the cursor keeps the filter's rows.
	var ids []int64
	query := "?limit=2"
	for pages := 0; ; pages++ {
		page := auditPage(t, s, mom, query)
		for _, e := range page.Items {
			ids = append(ids, e.ID)
		}
		if page.NextCursor == "" {
			if pages != 2 {
				t.Errorf("%d pages, want 3", pages+1)
			}
			break
		}
		query = "?limit=2&cursor=" + page.NextCursor
	}
	if len(ids) != 5 || !slices.IsSortedFunc(ids, func(a, b int64) int { return int(b - a) }) {
		t.Errorf("paged ids = %v", ids)
	}
	page := auditPage(t, s, mom, "?actorKind=parent&limit=2")
	if next := auditPage(t, s, mom, "?actorKind=parent&limit=2&cursor="+page.NextCursor); len(next.Items) != 1 ||
		next.Items[0].Action != "invite_created" || next.NextCursor != "" {
		t.Errorf("filtered second page = %+v", next)
	}
}
