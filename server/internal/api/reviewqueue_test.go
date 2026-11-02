package api_test

import (
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/horizon"
)

type queueItemJSON struct {
	Instance struct {
		ID             int64
		ChoreName      string
		KidID          int64
		CompletedBy    int64
		CompletedAt    time.Time
		RejectionCount int
		LastRejection  *struct{ Reason, Text string }
	}
	KidName string
	Steps   []struct {
		Text string
		Done bool
	}
	Submission *evidenceJSON
}

type queueJSON struct {
	Items      []queueItemJSON
	NextCursor string
}

// reviewEnv has four submissions, in this order a minute apart: Lawn (sam-2, a photo), Bed (sam, a
// photo and a ticked checklist), Desk (sam, a note only) and Toys (sam-2, a note only).
type reviewEnv struct {
	*evidenceEnv
	lawn, bed, desk, toys int64
	lawnPhoto             int64
}

func newReviewEnv(t *testing.T) *reviewEnv {
	t.Helper()
	e := &reviewEnv{evidenceEnv: newEvidenceEnv(t)}
	e.lawn = e.chore("Lawn", e.sam2ID, true, map[string]any{"photos": 1})
	e.bed = e.chore("Bed", e.samID, true, map[string]any{"photos": 1, "checklist": true}, "Strip", "Remake")
	e.desk = e.chore("Desk", e.samID, true, map[string]any{"photos": 0, "note": true})
	e.toys = e.chore("Toys", e.sam2ID, true, map[string]any{"photos": 0, "note": true})
	e.lawnPhoto = e.newPhoto(e.sam2)
	step := func() { e.clk.Advance(time.Minute) }
	wantCode(t, "lawn", e.complete(e.lawn, e.sam2, map[string]any{"photos": []any{kidPhoto(e.lawnPhoto, "", shotToday)}}), 200, "")
	step()
	wantCode(t, "bed", e.complete(e.bed, e.sam, map[string]any{"checklist": []int{0, 1}, "note": "Done",
		"photos": []any{kidPhoto(e.newPhoto(e.sam), "", shotToday)}}), 200, "")
	step()
	wantCode(t, "desk", e.complete(e.desk, e.sam, map[string]any{"note": "Tidy"}), 200, "")
	step()
	wantCode(t, "toys", e.complete(e.toys, e.sam2, map[string]any{"note": "Put away"}), 200, "")
	return e
}

func (e *reviewEnv) queue(query string, auth any) queueJSON {
	e.t.Helper()
	rec := e.call("GET", "/api/v1/review-queue"+query, auth, nil)
	wantCode(e.t, "queue "+query, rec, 200, "")
	return decode[queueJSON](e.t, rec)
}

func queueIDs(items []queueItemJSON) []int64 {
	out := []int64{}
	for _, i := range items {
		out = append(out, i.Instance.ID)
	}
	return out
}

func (e *reviewEnv) wantQueue(query string, want ...int64) {
	e.t.Helper()
	if got := queueIDs(e.queue(query, e.mom).Items); !slices.Equal(got, want) {
		e.t.Errorf("queue %q = %v, want %v", query, got, want)
	}
}

func TestReviewQueueListsOldestFirstWithEvidence(t *testing.T) {
	e := newReviewEnv(t)
	q := e.queue("", e.mom)
	if got, want := queueIDs(q.Items), []int64{e.lawn, e.bed, e.desk, e.toys}; !slices.Equal(got, want) {
		t.Fatalf("queue = %v, want oldest submission first: %v", got, want)
	}
	for _, i := range q.Items {
		if i.KidName == "" || i.Instance.CompletedBy != i.Instance.KidID {
			t.Errorf("item %d has no kid info: %+v", i.Instance.ID, i)
		}
	}

	bed := q.Items[1]
	if len(bed.Steps) != 2 || bed.Steps[0].Text != "Strip" || !bed.Steps[0].Done || !bed.Steps[1].Done {
		t.Errorf("bed steps: %+v", bed.Steps)
	}
	if bed.Submission == nil || bed.Submission.Note != "Done" || bed.Submission.Attempt != 1 || len(bed.Submission.Photos) != 1 ||
		bed.Submission.Photos[0].ThumbURL == "" || bed.Submission.Photos[0].Expired {
		t.Errorf("bed submission: %+v", bed.Submission)
	}
	// The signed thumbnail link works.
	u, err := url.Parse(bed.Submission.Photos[0].ThumbURL)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do("GET", u.RequestURI(), nil); rec.Code != 200 {
		t.Errorf("thumbnail: %d", rec.Code)
	}
	if desk := q.Items[2]; len(desk.Steps) != 0 || desk.Submission == nil || desk.Submission.Note != "Tidy" || len(desk.Submission.Photos) != 0 {
		t.Errorf("desk: %+v", desk)
	}

	// Retention deleting a photo marks it expired instead of linking it.
	if _, err := e.store.SQL.ExecContext(t.Context(), `UPDATE media SET status = 'expired' WHERE id = ?`, e.lawnPhoto); err != nil {
		t.Fatal(err)
	}
	if p := e.queue("", e.mom).Items[0].Submission.Photos[0]; !p.Expired || p.URL != "" || p.ThumbURL != "" {
		t.Errorf("expired photo: %+v", p)
	}
}

func TestReviewQueueFilters(t *testing.T) {
	e := newReviewEnv(t)
	e.wantQueue(fmt.Sprintf("?kidId=%d", e.samID), e.bed, e.desk)
	e.wantQueue(fmt.Sprintf("?kidId=%d", e.sam2ID), e.lawn, e.toys)
	choreOf := e.queue("", e.mom).Items[3].Instance
	var toysChore int64
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT chore_id FROM chore_instances WHERE id = ?`, choreOf.ID).Scan(&toysChore); err != nil {
		t.Fatal(err)
	}
	e.wantQueue(fmt.Sprintf("?choreId=%d", toysChore), e.toys)
	e.wantQueue("?media=with", e.lawn, e.bed)
	e.wantQueue("?media=without", e.desk, e.toys)
	e.wantQueue(fmt.Sprintf("?media=with&kidId=%d", e.samID), e.bed)
	e.wantQueue("?kidId=999")
	wantCode(t, "bad media", e.call("GET", "/api/v1/review-queue?media=maybe", e.mom, nil), 422, "validation_failed")

	// Only submitted instances are queued.
	wantCode(t, "approve", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/approval", e.lawn), e.mom, nil), 200, "")
	e.wantQueue("", e.bed, e.desk, e.toys)
}

func TestReviewQueuePaging(t *testing.T) {
	e := newReviewEnv(t)
	first := e.queue("?limit=3", e.mom)
	if !slices.Equal(queueIDs(first.Items), []int64{e.lawn, e.bed, e.desk}) || first.NextCursor == "" {
		t.Fatalf("first page: %v next %q", queueIDs(first.Items), first.NextCursor)
	}
	// Approving an item already served doesn't make the cursor skip or repeat anything.
	wantCode(t, "approve", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/approval", e.lawn), e.mom, nil), 200, "")
	second := e.queue("?limit=3&cursor="+url.QueryEscape(first.NextCursor), e.mom)
	if !slices.Equal(queueIDs(second.Items), []int64{e.toys}) || second.NextCursor != "" {
		t.Errorf("second page: %v next %q", queueIDs(second.Items), second.NextCursor)
	}
	wantCode(t, "bad cursor", e.call("GET", "/api/v1/review-queue?cursor=nope", e.mom, nil), 400, "invalid_cursor")
}

func TestReviewQueueShowsRejections(t *testing.T) {
	e := newReviewEnv(t)
	wantCode(t, "reject", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/rejection", e.desk), e.mom,
		map[string]any{"reason": "needs_redo", "text": "Wipe it too"}), 200, "")
	e.wantQueue("", e.lawn, e.bed, e.toys)
	e.clk.Advance(time.Minute)
	wantCode(t, "resubmit", e.complete(e.desk, e.sam, map[string]any{"note": "Wiped"}), 200, "")
	q := e.queue("", e.mom)
	if len(q.Items) != 4 || q.Items[3].Instance.ID != e.desk {
		t.Fatalf("a resubmission queues at the back: %v", queueIDs(q.Items))
	}
	desk := q.Items[3]
	if desk.Instance.RejectionCount != 1 || desk.Instance.LastRejection == nil || desk.Instance.LastRejection.Reason != "needs_redo" ||
		desk.Instance.LastRejection.Text != "Wipe it too" {
		t.Errorf("rejection: %+v", desk.Instance)
	}
	if desk.Submission == nil || desk.Submission.Attempt != 2 || desk.Submission.Note != "Wiped" {
		t.Errorf("the latest submission shows: %+v", desk.Submission)
	}
	// The quick reasons are a fixed list.
	wantCode(t, "unknown reason", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/rejection", e.bed), e.mom,
		map[string]any{"reason": "meh"}), 422, "validation_failed")
}

type bulkResultsJSON struct {
	Results []struct {
		InstanceID int64
		Approved   bool
		Code       string
	}
}

func TestBulkApprove(t *testing.T) {
	e := newReviewEnv(t)
	wantCode(t, "approve one", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/approval", e.lawn), e.mom, nil), 200, "")
	rec := e.call("POST", "/api/v1/review-queue/approvals", e.mom, map[string]any{"instanceIds": []int64{e.bed, e.lawn, 99999, e.desk, e.bed}})
	wantCode(t, "bulk", rec, 200, "")
	got := decode[bulkResultsJSON](t, rec).Results
	want := []struct {
		id       int64
		approved bool
		code     string
	}{{e.bed, true, ""}, {e.lawn, false, "already_approved"}, {99999, false, "instance_not_found"}, {e.desk, true, ""}}
	if len(got) != len(want) {
		t.Fatalf("results: %+v", got)
	}
	for i, w := range want {
		if g := got[i]; g.InstanceID != w.id || g.Approved != w.approved || g.Code != w.code {
			t.Errorf("result %d = %+v, want %+v", i, g, w)
		}
	}
	// The valid ones are approved (and credited) despite the failures between them.
	e.wantQueue("", e.toys)
	var approved, entries int
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT count(*) FROM chore_instances WHERE status = 'approved' AND id IN (?, ?)`, e.bed, e.desk).Scan(&approved); err != nil || approved != 2 {
		t.Errorf("approved = %d, %v", approved, err)
	}
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT count(*) FROM points_entries`).Scan(&entries); err != nil || entries < 3 {
		t.Errorf("points entries = %d, %v", entries, err)
	}
}

func TestBulkApproveLimit(t *testing.T) {
	e := newReviewEnv(t)
	many := func(n int) []int64 {
		out := make([]int64, n)
		for i := range out {
			out[i] = int64(i + 1000)
		}
		return out
	}
	wantCode(t, "101 ids", e.call("POST", "/api/v1/review-queue/approvals", e.mom, map[string]any{"instanceIds": many(101)}), 422, "validation_failed")
	wantCode(t, "no ids", e.call("POST", "/api/v1/review-queue/approvals", e.mom, map[string]any{"instanceIds": []int64{}}), 422, "validation_failed")
	rec := e.call("POST", "/api/v1/review-queue/approvals", e.mom, map[string]any{"instanceIds": many(100)})
	wantCode(t, "100 ids", rec, 200, "")
	if n := len(decode[bulkResultsJSON](t, rec).Results); n != 100 {
		t.Errorf("results = %d", n)
	}
}

func TestReviewQueueIsParentsOnly(t *testing.T) {
	e := newReviewEnv(t)
	wantCode(t, "kid lists", e.call("GET", "/api/v1/review-queue", e.sam, nil), 403, "")
	wantCode(t, "kid bulk approves", e.call("POST", "/api/v1/review-queue/approvals", e.sam,
		map[string]any{"instanceIds": []int64{e.bed}}), 403, "")
	e.wantQueue("", e.lawn, e.bed, e.desk, e.toys)
}

func TestReviewQueueSharedInstanceMatchesEveryMember(t *testing.T) {
	e := newReviewEnv(t)
	rec := e.do("POST", "/api/v1/chores", map[string]any{
		"name": "Tidy", "points": 4, "requiresApproval": true, "evidence": map[string]any{"photos": 0, "note": true},
		"schedule":   map[string]any{"type": "daily", "startDate": "2026-10-31"},
		"assignment": map[string]any{"mode": "shared", "kidIds": []int64{e.samID, e.sam2ID}, "sharedPoints": "split_evenly"},
	}, e.mom)
	wantCode(t, "create shared", rec, 201, "")
	if _, err := horizon.New(e.store, e.clk).Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var shared int64
	for _, i := range decode[struct{ Items []instanceJSON }](t, e.do("GET", "/api/v1/instances", nil, e.mom)).Items {
		if i.KidID == 0 && i.Date == "2026-10-31" {
			shared = i.ID
		}
	}
	if shared == 0 {
		t.Fatal("no shared instance")
	}
	e.clk.Advance(time.Minute)
	wantCode(t, "complete", e.complete(shared, e.sam, map[string]any{"note": "Together"}), 200, "")
	e.wantQueue(fmt.Sprintf("?kidId=%d", e.samID), e.bed, e.desk, shared)
	e.wantQueue(fmt.Sprintf("?kidId=%d", e.sam2ID), e.lawn, e.toys, shared)
	if got := e.queue(fmt.Sprintf("?kidId=%d", e.sam2ID), e.mom).Items[2]; got.Instance.CompletedBy != e.samID || got.KidName == "" {
		t.Errorf("the shared item shows its completer: %+v", got)
	}
}

func TestReviewQueueLimitBounds(t *testing.T) {
	e := newReviewEnv(t)
	for _, limit := range []string{"0", "101", "-1"} {
		wantCode(t, "limit "+limit, e.call("GET", "/api/v1/review-queue?limit="+limit, e.mom, nil), 422, "validation_failed")
	}
	if n := len(e.queue("?limit=100", e.mom).Items); n != 4 {
		t.Errorf("limit 100 returned %d", n)
	}
	if n := len(e.queue("?limit=1", e.mom).Items); n != 1 {
		t.Errorf("limit 1 returned %d", n)
	}
}

func TestReviewQueueRefusesSharedDevices(t *testing.T) {
	e := newReviewEnv(t)
	_, device := pairDevice(t, e.setupServer, pair(t, e.setupServer, e.mom, "").Code)
	wantCode(t, "shared device lists", e.call("GET", "/api/v1/review-queue", device, nil), 403, "")
	wantCode(t, "shared device bulk approves", e.call("POST", "/api/v1/review-queue/approvals", device,
		map[string]any{"instanceIds": []int64{e.bed}}), 403, "")
	e.wantQueue("", e.lawn, e.bed, e.desk, e.toys)
}
