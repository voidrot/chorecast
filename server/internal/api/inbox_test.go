package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/auth"
)

type inboxJSON struct {
	Items []struct {
		ID      int64
		Kind    string
		KidID   int64
		RefKind string
		RefID   int64
		Data    struct{ ChoreName string }
		ReadAt  *time.Time
	}
	NextCursor  string
	UnreadCount int64
}

type inboxEnv struct {
	*setupServer
	mom           *http.Cookie
	momID         int64
	sam, sam2     string // kid device tokens
	samID, sam2ID int64
}

func newInboxEnv(t *testing.T) *inboxEnv {
	t.Helper()
	s := newAuthServer(t)
	e := &inboxEnv{setupServer: s, mom: sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))}
	_, e.sam = pairDevice(t, s, pair(t, s, e.mom, "sam").Code)
	_, e.sam2 = pairDevice(t, s, pair(t, s, e.mom, "sam-2").Code)
	e.momID = decode[struct{ UserID int64 }](t, s.do("GET", "/api/v1/me", nil, e.mom)).UserID
	e.samID = decode[struct{ UserID int64 }](t, s.bearer("GET", "/api/v1/me", e.sam, nil)).UserID
	e.sam2ID = decode[struct{ UserID int64 }](t, s.bearer("GET", "/api/v1/me", e.sam2, nil)).UserID
	return e
}

// notify stores a notification for recipient the way the inbox service does (that path is
// tested in internal/inbox), a minute after the last one, and returns its id.
func (e *inboxEnv) notify(recipient int64, kind string) int64 {
	e.t.Helper()
	e.clk.Advance(time.Minute)
	res, err := e.store.SQL.ExecContext(e.t.Context(), `INSERT INTO notifications
		(recipient_id, kind, kid_id, ref_kind, ref_id, event_id, data, created_at) VALUES (?, ?, ?, 'instance', 7, ?, '{"choreName":"Dishes"}', ?)`,
		recipient, kind, e.samID, e.clk.Now().UnixNano(), e.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *inboxEnv) inbox(auth any, query string) inboxJSON {
	e.t.Helper()
	var rec = e.call(auth, "GET", "/api/v1/inbox"+query)
	wantCode(e.t, "inbox"+query, rec, 200, "")
	return decode[inboxJSON](e.t, rec)
}

func (e *inboxEnv) call(auth any, method, path string) *httptest.ResponseRecorder {
	if token, ok := auth.(string); ok {
		return e.bearer(method, path, token, nil)
	}
	return e.do(method, path, nil, auth.(*http.Cookie))
}

func ids(j inboxJSON) []int64 {
	out := []int64{}
	for _, i := range j.Items {
		out = append(out, i.ID)
	}
	return out
}

// Each caller sees only their own notifications: a parent's, a kid's on their own device, and the
// picked kid's on a shared tablet; a tablet with nobody picked has none.
func TestInboxIsPerRecipient(t *testing.T) {
	e := newInboxEnv(t)
	forMom := e.notify(e.momID, "chore_submitted")
	forSam := e.notify(e.samID, "chore_approved")
	forSam2 := e.notify(e.sam2ID, "chore_missed")

	for who, c := range map[string]struct {
		auth any
		want int64
	}{"mom": {e.mom, forMom}, "sam": {e.sam, forSam}, "sam-2": {e.sam2, forSam2}} {
		if got := ids(e.inbox(c.auth, "")); !slices.Equal(got, []int64{c.want}) {
			t.Errorf("%s sees %v, want [%d]", who, got, c.want)
		}
	}

	// A shared tablet: sam's picked profile reads sam's inbox, the bare device has none.
	wantCode(t, "sam's PIN", e.do("PUT", "/api/v1/kids/sam/pin", map[string]string{"pin": "2468"}, e.mom), 204, "")
	_, tablet := pairDevice(t, e.setupServer, pair(t, e.setupServer, e.mom, "").Code)
	rec := e.bearer("POST", "/api/v1/devices/session", tablet, map[string]string{"kid": "sam", "pin": "2468"})
	wantCode(t, "pick sam", rec, 200, "")
	samOnTablet := decode[struct{ Token string }](t, rec).Token
	if got := ids(e.inbox(samOnTablet, "")); !slices.Equal(got, []int64{forSam}) {
		t.Errorf("sam on the tablet sees %v, want [%d]", got, forSam)
	}
	wantCode(t, "tablet with nobody picked", e.bearer("GET", "/api/v1/inbox", tablet, nil), 403, "insufficient_scope")
	wantCode(t, "anonymous", e.do("GET", "/api/v1/inbox", nil), 401, "")

	// A parent elevated on the tablet with their PIN reads their own inbox, not the kid's.
	wantCode(t, "mom's parent PIN", e.do("PUT", "/api/v1/account/pin", map[string]string{"password": ownerPassword, "pin": "1357"}, e.mom), 204, "")
	rec = e.bearer("POST", "/api/v1/devices/elevate", tablet, map[string]any{"parentId": e.momID, "pin": "1357"})
	wantCode(t, "elevate mom", rec, 200, "")
	momOnTablet := decode[struct{ Token string }](t, rec).Token
	if got := ids(e.inbox(momOnTablet, "")); !slices.Equal(got, []int64{forMom}) {
		t.Errorf("mom on the tablet sees %v, want [%d]", got, forMom)
	}

	// Displays and API keys have no user to own an inbox.
	tokens := auth.NewStore(e.store.Queries, e.clk)
	for name, kind := range map[string]auth.TokenKind{"display": auth.TokenDisplay, "api key": auth.TokenAPI} {
		token, _, err := tokens.Issue(t.Context(), auth.IssueParams{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, name+" lists", e.bearer("GET", "/api/v1/inbox", token, nil), 403, "insufficient_scope")
		wantCode(t, name+" marks all", e.bearer("POST", "/api/v1/inbox/read-all", token, nil), 403, "insufficient_scope")
	}

	// Marking reads is just as scoped: it is a 404 to another recipient, and changes nothing.
	wantCode(t, "sam-2 marks sam's", e.call(e.sam2, "POST", fmt.Sprintf("/api/v1/inbox/%d/read", forSam)), 404, "notification_not_found")
	wantCode(t, "mom marks sam's", e.call(e.mom, "POST", fmt.Sprintf("/api/v1/inbox/%d/read", forSam)), 404, "notification_not_found")
	wantCode(t, "no such", e.call(e.sam, "POST", "/api/v1/inbox/9999/read"), 404, "notification_not_found")
	wantCode(t, "mom marks all", e.call(e.mom, "POST", "/api/v1/inbox/read-all"), 204, "")
	if got := e.inbox(e.sam, ""); got.UnreadCount != 1 || got.Items[0].ReadAt != nil {
		t.Errorf("sam's inbox after the others' marks = %+v", got)
	}
}

// The inbox is newest first and paged by cursor; the unread count covers all pages and drops
// as notifications are read.
func TestInboxPagingAndUnread(t *testing.T) {
	e := newInboxEnv(t)
	var all []int64
	for range 5 {
		all = append(all, e.notify(e.samID, "chore_approved"))
	}
	e.notify(e.momID, "chore_submitted")
	newest := []int64{all[4], all[3], all[2], all[1], all[0]}

	p1 := e.inbox(e.sam, "?limit=2")
	if !slices.Equal(ids(p1), newest[:2]) || p1.NextCursor == "" || p1.UnreadCount != 5 {
		t.Fatalf("page 1 = %v cursor %q unread %d", ids(p1), p1.NextCursor, p1.UnreadCount)
	}
	p2 := e.inbox(e.sam, "?limit=2&cursor="+p1.NextCursor)
	p3 := e.inbox(e.sam, "?limit=2&cursor="+p2.NextCursor)
	if !slices.Equal(ids(p2), newest[2:4]) || !slices.Equal(ids(p3), newest[4:]) || p3.NextCursor != "" {
		t.Fatalf("pages = %v, %v, %v", ids(p1), ids(p2), ids(p3))
	}
	wantCode(t, "bad cursor", e.call(e.sam, "GET", "/api/v1/inbox?cursor=junk"), 400, "invalid_cursor")

	// Reading one lowers the count, and reading it again keeps the first read time.
	path := fmt.Sprintf("/api/v1/inbox/%d/read", all[2])
	wantCode(t, "mark one", e.call(e.sam, "POST", path), 204, "")
	first := e.inbox(e.sam, "").Items[2].ReadAt
	e.clk.Advance(time.Hour)
	wantCode(t, "mark it again", e.call(e.sam, "POST", path), 204, "")
	got := e.inbox(e.sam, "?limit=2")
	if got.UnreadCount != 4 || first == nil || !e.inbox(e.sam, "").Items[2].ReadAt.Equal(*first) {
		t.Errorf("after marking one: unread %d, read time %v then %v", got.UnreadCount, first, e.inbox(e.sam, "").Items[2].ReadAt)
	}

	// Mark all clears Sam's, and only Sam's.
	wantCode(t, "mark all", e.call(e.sam, "POST", "/api/v1/inbox/read-all"), 204, "")
	if n := e.inbox(e.sam, "").UnreadCount; n != 0 {
		t.Errorf("sam's unread after mark all = %d", n)
	}
	if n := e.inbox(e.mom, "").UnreadCount; n != 1 {
		t.Errorf("mom's unread after sam's mark all = %d, want 1", n)
	}
}
