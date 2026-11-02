package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/evidence"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/media"
)

// The test server's day is 31 October 2026 in Chicago (day end 21:00), which began at 21:00 the
// evening before (02:00 UTC); it's 07:00 there now.
const (
	shotToday     = "2026-10-31T11:00:00Z" // a capture this morning
	shotYesterday = "2026-10-31T01:00:00Z" // 20:00 the evening before: yesterday's day
)

type evidenceJSON struct {
	Attempt     int64
	KidID       int64
	SubmittedBy int64
	Checklist   []int
	Note        string
	Photos      []struct {
		MediaID    int64
		Role       string
		CapturedAt time.Time
		CaptureID  string
		Expired    bool
		URL        string
		ThumbURL   string
	}
}

type evidenceEnv struct {
	*mediaEnv
	samID, sam2ID int64
	shot          int // makes each photo different
}

func newEvidenceEnv(t *testing.T) *evidenceEnv {
	t.Helper()
	e := &evidenceEnv{mediaEnv: newMediaEnv(t, config.Config{})}
	sam, _ := e.store.GetKidBySlug(t.Context(), sqlNull("sam"))
	sam2, _ := e.store.GetKidBySlug(t.Context(), sqlNull("sam-2"))
	e.samID, e.sam2ID = sam.ID, sam2.ID
	return e
}

// chore creates a daily chore for kid with the given evidence rules and steps, regenerates the
// horizon and returns today's instance.
func (e *evidenceEnv) chore(name string, kid int64, approval bool, rules map[string]any, steps ...string) int64 {
	e.t.Helper()
	rec := e.do("POST", "/api/v1/chores", map[string]any{
		"name": name, "points": 3, "requiresApproval": approval, "evidence": rules, "steps": steps,
		"schedule":   map[string]any{"type": "daily", "startDate": "2026-10-31"},
		"assignment": map[string]any{"mode": "fixed", "kidIds": []int64{kid}},
	}, e.mom)
	wantCode(e.t, "create "+name, rec, 201, "")
	chore := decode[struct{ ID int64 }](e.t, rec).ID
	if _, err := horizon.New(e.store, e.clk).Generate(e.t.Context()); err != nil {
		e.t.Fatal(err)
	}
	for _, i := range decode[struct{ Items []instanceJSON }](e.t, e.do("GET", "/api/v1/instances", nil, e.mom)).Items {
		if i.ChoreID == chore && i.KidID == kid && i.Date == "2026-10-31" {
			return i.ID
		}
	}
	e.t.Fatalf("no instance of %s today", name)
	return 0
}

// newPhoto uploads a new, different photo as auth and returns its id.
func (e *evidenceEnv) newPhoto(auth any) int64 {
	e.t.Helper()
	e.shot++
	return e.uploaded(auth, photo(e.t, 80, 20+e.shot)).ID
}

// call sends a JSON request as auth (a bearer token or a session cookie).
func (e *evidenceEnv) call(method, path string, auth any, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	ct := ""
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			e.t.Fatal(err)
		}
		ct = "application/json"
	}
	return e.raw(method, path, auth, &buf, ct)
}

func (e *evidenceEnv) complete(id int64, auth any, body map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	path := fmt.Sprintf("/api/v1/instances/%d/completion", id)
	if body == nil {
		return e.call("POST", path, auth, nil)
	}
	return e.call("POST", path, auth, body)
}

func (e *evidenceEnv) evidence(id int64, auth any) []evidenceJSON {
	e.t.Helper()
	rec := e.call("GET", fmt.Sprintf("/api/v1/instances/%d/evidence", id), auth, nil)
	wantCode(e.t, "read evidence", rec, 200, "")
	return decode[struct{ Items []evidenceJSON }](e.t, rec).Items
}

func kidPhoto(id int64, role, at string) map[string]any {
	p := map[string]any{"mediaId": id, "capturedAt": at, "captureId": fmt.Sprintf("cap-%d", id)}
	if role != "" {
		p["role"] = role
	}
	return p
}

// wantLocations checks a 422 lists a problem at each location.
func wantLocations(t *testing.T, what string, rec *httptest.ResponseRecorder, locs ...string) {
	t.Helper()
	wantCode(t, what, rec, 422, "validation_failed")
	var got []string
	for _, d := range decode[struct{ Errors []struct{ Location string } }](t, rec).Errors {
		got = append(got, d.Location)
	}
	for _, l := range locs {
		if !slices.Contains(got, l) {
			t.Errorf("%s: problems at %v, want one at %s", what, got, l)
		}
	}
}

func (e *evidenceEnv) mediaRow(id int64) (ownerKind string, owner sql.NullInt64, status string, expires sql.NullString) {
	e.t.Helper()
	if err := e.store.SQL.QueryRowContext(e.t.Context(),
		`SELECT owner_kind, owner_id, status, expires_at FROM media WHERE id = ?`, id).Scan(&ownerKind, &owner, &status, &expires); err != nil {
		e.t.Fatal(err)
	}
	return ownerKind, owner, status, expires
}

func TestCompletionEnforcesEvidenceRules(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Dishes", e.samID, true,
		map[string]any{"photos": 2, "beforeAfter": true, "checklist": true, "note": true}, "Wash", "Dry")
	before, after := e.newPhoto(e.sam), e.newPhoto(e.sam)
	good := func() map[string]any {
		return map[string]any{"checklist": []int{1, 0}, "note": "All clean",
			"photos": []any{kidPhoto(before, "before", shotToday), kidPhoto(after, "after", shotToday)}}
	}
	with := func(k string, v any) map[string]any { b := good(); b[k] = v; return b }

	wantLocations(t, "nothing", e.complete(id, e.sam, nil), "body.checklist", "body.note", "body.photos")
	wantLocations(t, "a step unticked", e.complete(id, e.sam, with("checklist", []int{0, 0})), "body.checklist")
	wantLocations(t, "no such step", e.complete(id, e.sam, with("checklist", []int{0, 1, 2})), "body.checklist[2]")
	wantLocations(t, "blank note", e.complete(id, e.sam, with("note", "   ")), "body.note")
	wantLocations(t, "one photo", e.complete(id, e.sam, with("photos", []any{kidPhoto(before, "before", shotToday)})), "body.photos")
	wantLocations(t, "no before and after", e.complete(id, e.sam, with("photos",
		[]any{kidPhoto(before, "", shotToday), kidPhoto(after, "after", shotToday)})), "body.photos")
	wantLocations(t, "the same photo twice", e.complete(id, e.sam, with("photos",
		[]any{kidPhoto(before, "before", shotToday), kidPhoto(before, "after", shotToday)})), "body.photos[1].mediaId")
	wantLocations(t, "no capture time", e.complete(id, e.sam, with("photos",
		[]any{map[string]any{"mediaId": before, "role": "before", "captureId": "c1"}, kidPhoto(after, "after", shotToday)})),
		"body.photos[0].capturedAt")
	wantLocations(t, "no capture id", e.complete(id, e.sam, with("photos",
		[]any{kidPhoto(before, "before", shotToday), map[string]any{"mediaId": after, "role": "after", "capturedAt": shotToday}})),
		"body.photos[1].captureId")
	wantLocations(t, "taken before the day began", e.complete(id, e.sam, with("photos",
		[]any{kidPhoto(before, "before", shotYesterday), kidPhoto(after, "after", shotToday)})), "body.photos[0].capturedAt")
	siblings := e.newPhoto(e.sam2)
	wantLocations(t, "a sibling's photo", e.complete(id, e.sam, with("photos",
		[]any{kidPhoto(before, "before", shotToday), kidPhoto(siblings, "after", shotToday)})), "body.photos[1].mediaId")
	moms := e.newPhoto(e.mom)
	wantLocations(t, "a parent's photo, completed by the kid", e.complete(id, e.sam, with("photos",
		[]any{kidPhoto(before, "before", shotToday), kidPhoto(moms, "after", shotToday)})), "body.photos[1].mediaId")
	wantLocations(t, "no such photo", e.complete(id, e.sam, with("photos",
		[]any{kidPhoto(before, "before", shotToday), kidPhoto(99999, "after", shotToday)})), "body.photos[1].mediaId")
	for _, m := range []int64{before, after} {
		if kind, _, _, _ := e.mediaRow(m); kind != "pending" {
			t.Fatalf("a refused completion attached media %d: %s", m, kind)
		}
	}

	rec := e.complete(id, e.sam, good())
	wantCode(t, "complete with every rule met", rec, 200, "")
	if got := decode[transitionJSON](t, rec); got.Status != "submitted" {
		t.Errorf("completed: %+v", got)
	}
	for _, m := range []int64{before, after} {
		kind, owner, status, expires := e.mediaRow(m)
		if kind != "evidence" || !owner.Valid || status != "ready" || expires.Valid {
			t.Errorf("media %d after completion: %s/%v %s expires %v; want evidence, no expiry", m, kind, owner, status, expires)
		}
		wantCode(t, "kid reads the attached photo as pending media", e.call("GET", fmt.Sprintf("/api/v1/media/%d", m), e.sam, nil), 404, "media_not_found")
	}

	ev := e.evidence(id, e.sam)
	if len(ev) != 1 || ev[0].Attempt != 1 || ev[0].KidID != e.samID || !slices.Equal(ev[0].Checklist, []int{0, 1}) ||
		ev[0].Note != "All clean" || len(ev[0].Photos) != 2 {
		t.Fatalf("evidence: %+v", ev)
	}
	p := ev[0].Photos[0]
	if p.MediaID != before || p.Role != "before" || p.CaptureID != fmt.Sprintf("cap-%d", before) || p.Expired ||
		!p.CapturedAt.Equal(time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)) || p.URL == "" || p.ThumbURL == "" {
		t.Errorf("first photo: %+v", p)
	}
	wantCode(t, "the photo link works", e.fetch(p.URL), 200, "")
	if got := e.evidence(id, e.mom); len(got) != 1 {
		t.Errorf("parent reads %d submissions", len(got))
	}
	wantCode(t, "a sibling reads it", e.call("GET", fmt.Sprintf("/api/v1/instances/%d/evidence", id), e.sam2, nil), 403, "forbidden")
}

func TestEvidenceResubmissionAndReuse(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 1})
	other := e.chore("Desk", e.samID, true, map[string]any{"photos": 1})
	data := photo(t, 64, 33)
	first := e.uploaded(e.sam, data).ID
	wantCode(t, "complete", e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(first, "", shotToday)}}), 200, "")

	// The photo is evidence now: neither it nor the same picture can be used again.
	wantLocations(t, "the same media on another chore", e.complete(other, e.sam,
		map[string]any{"photos": []any{kidPhoto(first, "", shotToday)}}), "body.photos[0].mediaId")
	wantCode(t, "the same picture uploaded again", e.upload(e.sam, data, "image/jpeg"), 409, "duplicate_media")

	wantCode(t, "reject", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/rejection", id), e.mom, map[string]any{"reason": "needs_redo"}), 200, "")
	wantLocations(t, "resubmit with the rejected photo", e.complete(id, e.sam,
		map[string]any{"photos": []any{kidPhoto(first, "", shotToday)}}), "body.photos[0].mediaId")
	second := e.newPhoto(e.sam)
	wantCode(t, "resubmit", e.complete(id, e.sam, map[string]any{"note": "Better", "photos": []any{kidPhoto(second, "", shotToday)}}), 200, "")
	ev := e.evidence(id, e.mom)
	if len(ev) != 2 || ev[0].Attempt != 1 || ev[1].Attempt != 2 || ev[0].Photos[0].MediaID != first ||
		ev[1].Photos[0].MediaID != second || ev[1].Note != "Better" {
		t.Fatalf("both submissions are kept: %+v", ev)
	}
}

func TestParentCompletesWithTheirOwnPhoto(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Lawn", e.samID, true, map[string]any{"photos": 1})
	moms := e.newPhoto(e.mom)
	// Parents' photos need no capture data and aren't held to the capture window (rule 5).
	wantCode(t, "parent completes for the kid", e.complete(id, e.mom, map[string]any{"photos": []any{map[string]any{"mediaId": moms}}}), 200, "")
	if ev := e.evidence(id, e.sam); len(ev) != 1 || ev[0].KidID != e.samID || ev[0].SubmittedBy == e.samID || len(ev[0].Photos) != 1 {
		t.Errorf("evidence: %+v", ev)
	}
}

// Video evidence isn't available yet: chores can't ask for it, and a chore that does anyway (set
// before this check) can't be completed.
func TestVideoEvidenceIsRefused(t *testing.T) {
	e := newEvidenceEnv(t)
	body := func(video bool) map[string]any {
		return map[string]any{"name": "Piano", "points": 1, "requiresApproval": true,
			"evidence":   map[string]any{"photos": 0, "video": video},
			"schedule":   map[string]any{"type": "daily", "startDate": "2026-10-31"},
			"assignment": map[string]any{"mode": "fixed", "kidIds": []int64{e.samID}}}
	}
	wantCode(t, "create a video chore", e.do("POST", "/api/v1/chores", body(true), e.mom), 422, "video_evidence_unsupported")
	rec := e.do("POST", "/api/v1/chores", body(false), e.mom)
	wantCode(t, "create", rec, 201, "")
	chore := decode[struct{ ID int64 }](t, rec).ID
	wantCode(t, "ask for a video on update", e.do("PUT", fmt.Sprintf("/api/v1/chores/%d", chore), body(true), e.mom), 422, "video_evidence_unsupported")

	id := e.chore("Drums", e.samID, true, map[string]any{"photos": 0})
	if _, err := e.store.SQL.ExecContext(t.Context(), `UPDATE chores SET evidence_video = 1`); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "complete a video chore", e.complete(id, e.sam, nil), 422, "video_evidence_unsupported")
}

// capturedAt can't be later than now (plus MaxCaptureSkew) or unstorable, for anyone's photo; a
// stored value that doesn't parse doesn't break the read.
func TestCaptureTimeBounds(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 1})
	m := e.newPhoto(e.sam)
	with := func(at string) map[string]any { return map[string]any{"photos": []any{kidPhoto(m, "", at)}} }
	for what, at := range map[string]string{
		"past the skew":             e.clk.Now().Add(evidence.MaxCaptureSkew + time.Minute).Format(time.RFC3339),
		"in year 10000 once in UTC": "9999-12-31T23:00:00-02:00",
	} {
		wantLocations(t, what, e.complete(id, e.sam, with(at)), "body.photos[0].capturedAt")
	}
	moms, lawn := e.newPhoto(e.mom), e.chore("Lawn", e.samID, true, map[string]any{"photos": 1})
	wantLocations(t, "a parent's photo from the future", e.complete(lawn, e.mom, map[string]any{"photos": []any{
		map[string]any{"mediaId": moms, "capturedAt": "9999-12-31T23:00:00-02:00"}}}), "body.photos[0].capturedAt")

	wantCode(t, "within the skew", e.complete(id, e.sam, with(e.clk.Now().Add(5*time.Minute).Format(time.RFC3339))), 200, "")
	if _, err := e.store.SQL.ExecContext(t.Context(),
		`UPDATE chore_instance_evidence_photos SET captured_at = '10000-01-01T01:00:00.000Z' WHERE media_id = ?`, m); err != nil {
		t.Fatal(err)
	}
	ev := e.evidence(id, e.mom)
	if len(ev) != 1 || len(ev[0].Photos) != 1 || !ev[0].Photos[0].CapturedAt.IsZero() || ev[0].Photos[0].URL == "" {
		t.Errorf("a bad stored capture time: %+v", ev)
	}
	if ev[0].Checklist == nil {
		t.Error("checklist is null; want []")
	}
}

// Kids read the submissions they made, or a shared instance's group's; parents read all.
func TestEvidenceReadScoping(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Dishes", e.samID, true, map[string]any{"photos": 0})
	path := func(action string) string { return fmt.Sprintf("/api/v1/instances/%d/%s", id, action) }
	notes := func(auth any) []string {
		t.Helper()
		var out []string
		for _, ev := range e.evidence(id, auth) {
			out = append(out, ev.Note)
		}
		return out
	}
	if got := notes(e.sam); len(got) != 0 {
		t.Errorf("the assignee before submitting: %v", got)
	}
	wantCode(t, "a sibling with nothing of theirs", e.call("GET", path("evidence"), e.sam2, nil), 403, "forbidden")

	wantCode(t, "Sam completes", e.complete(id, e.sam, map[string]any{"note": "Sam's"}), 200, "")
	wantCode(t, "reject", e.call("POST", path("rejection"), e.mom, map[string]any{"reason": "needs_redo"}), 200, "")
	wantCode(t, "reassign to Sam 2", e.call("POST", path("reassignment"), e.mom, map[string]any{"kidId": e.sam2ID}), 200, "")
	if got := notes(e.sam2); len(got) != 0 {
		t.Errorf("the new assignee sees the earlier kid's evidence: %v", got)
	}
	if got := notes(e.sam); !slices.Equal(got, []string{"Sam's"}) {
		t.Errorf("the earlier kid keeps theirs: %v", got)
	}
	wantCode(t, "Sam 2 completes", e.complete(id, e.sam2, map[string]any{"note": "Sam 2's"}), 200, "")
	if got := notes(e.sam2); !slices.Equal(got, []string{"Sam 2's"}) {
		t.Errorf("Sam 2 sees %v", got)
	}
	if got := notes(e.sam); !slices.Equal(got, []string{"Sam's"}) {
		t.Errorf("Sam sees %v", got)
	}
	if got := notes(e.mom); !slices.Equal(got, []string{"Sam's", "Sam 2's"}) {
		t.Errorf("a parent sees %v", got)
	}

	// A shared instance's members see the group's submissions.
	rec := e.do("POST", "/api/v1/chores", map[string]any{"name": "Tidy", "points": 2, "requiresApproval": true,
		"evidence": map[string]any{"photos": 0}, "schedule": map[string]any{"type": "daily", "startDate": "2026-10-31"},
		"assignment": map[string]any{"mode": "shared", "kidIds": []int64{e.samID, e.sam2ID}, "sharedPoints": "split_evenly"}}, e.mom)
	wantCode(t, "create shared", rec, 201, "")
	chore := decode[struct{ ID int64 }](t, rec).ID
	if _, err := horizon.New(e.store, e.clk).Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var shared int64
	for _, i := range decode[struct{ Items []instanceJSON }](t, e.do("GET", "/api/v1/instances", nil, e.mom)).Items {
		if i.ChoreID == chore && i.Date == "2026-10-31" {
			shared = i.ID
		}
	}
	wantCode(t, "Sam completes the shared one", e.complete(shared, e.sam, map[string]any{"note": "Tidied"}), 200, "")
	if got := e.evidence(shared, e.sam2); len(got) != 1 || got[0].Note != "Tidied" {
		t.Errorf("a group member sees %+v", got)
	}
}

func TestPendingUploads(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 1})
	data := photo(t, 64, 40)
	m := e.uploaded(e.sam, data)
	key := e.storageKey(m.ID)

	if _, _, _, exp := e.mediaRow(m.ID); exp.String != db.Timestamp(e.clk.Now().Add(media.PendingTTL)) {
		t.Errorf("pending upload expires_at = %v, want upload + PendingTTL", exp)
	}
	// Not attached within PendingTTL: it can't be used. (Moving the clock a day on would close the
	// chore's day, so the upload is made older instead.)
	if _, err := e.store.SQL.ExecContext(t.Context(), `UPDATE media SET expires_at = ? WHERE id = ?`,
		db.Timestamp(e.clk.Now().Add(-time.Minute)), m.ID); err != nil {
		t.Fatal(err)
	}
	wantLocations(t, "attach an expired upload", e.complete(id, e.sam,
		map[string]any{"photos": []any{kidPhoto(m.ID, "", shotToday)}}), "body.photos[0].mediaId")
	// The kid uploading that picture again replaces the abandoned upload, before retention runs.
	again := e.uploaded(e.sam, data)
	if again.ID == m.ID {
		t.Fatal("the re-upload returned the abandoned media")
	}
	if _, _, status, _ := e.mediaRow(m.ID); status != "deleting" {
		t.Errorf("the replaced upload is %s, want claimed for deletion", status)
	}
	wantCode(t, "another kid, same picture", e.upload(e.sam2, data, "image/jpeg"), 409, "duplicate_media")
	if res := e.expire(e.local()); res.Expired != 1 || res.Failed != 0 {
		t.Fatalf("expire: %+v, want the replaced upload", res)
	}
	if _, _, status, _ := e.mediaRow(m.ID); status != "expired" {
		t.Errorf("abandoned upload status %s", status)
	}
	e.wantFiles(key, false)

	// A kid may hold MaxPendingUploads attachable uploads (the re-upload is one), and a retry is
	// never refused for the cap.
	for range media.MaxPendingUploads - 1 {
		e.newPhoto(e.sam)
	}
	e.shot++
	wantCode(t, "one upload over the cap", e.upload(e.sam, photo(t, 80, 20+e.shot), "image/jpeg"), 429, "rate_limited")
	if retry := e.uploaded(e.sam, data); retry.ID != again.ID {
		t.Errorf("a retry at the cap: media %d, want %d", retry.ID, again.ID)
	}
	e.newPhoto(e.sam2) // per kid
	e.newPhoto(e.mom)  // parents aren't capped
}

func (e *evidenceEnv) storageKey(id int64) string {
	e.t.Helper()
	var key string
	if err := e.store.SQL.QueryRowContext(e.t.Context(), `SELECT storage_key FROM media WHERE id = ?`, id).Scan(&key); err != nil {
		e.t.Fatal(err)
	}
	return key
}

// wantFiles checks whether the photo and thumbnail stored under key exist.
func (e *evidenceEnv) wantFiles(key string, exist bool) {
	e.t.Helper()
	for _, k := range []string{key, media.ThumbKey(key)} {
		_, err := os.Stat(filepath.Join(e.dataDir, "media", k[0:2], k[2:4], k))
		if (err == nil) != exist {
			e.t.Errorf("file %s exists = %v, want %v", k, err == nil, exist)
		}
	}
}

func (e *evidenceEnv) local() media.Storage { return media.NewLocal(e.dataDir, "", nil) }

// expire runs the retention job against store.
func (e *evidenceEnv) expire(store media.Storage) media.ExpireResult {
	e.t.Helper()
	res, err := media.Expire(e.t.Context(), e.store, e.clk, store, slog.New(slog.DiscardHandler))
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// hookStore calls before ahead of each Delete and fails the Deletes of the keys in fail.
type hookStore struct {
	media.Storage
	before func(key string)
	fail   map[string]bool
}

func (s *hookStore) Delete(ctx context.Context, key string) error {
	if s.before != nil {
		s.before(key)
	}
	if s.fail[key] {
		return errors.New("permission denied")
	}
	return s.Storage.Delete(ctx, key)
}

func TestEvidenceRetention(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 2})
	desk := e.chore("Desk", e.samID, false, map[string]any{"photos": 1}) // no approval needed
	kept, gone, auto := e.newPhoto(e.sam), e.newPhoto(e.sam), e.newPhoto(e.sam)
	wantCode(t, "complete", e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(kept, "", shotToday), kidPhoto(gone, "", shotToday)}}), 200, "")
	wantCode(t, "complete without approval", e.complete(desk, e.sam, map[string]any{"photos": []any{kidPhoto(auto, "", shotToday)}}), 200, "")
	if _, err := e.store.SQL.ExecContext(t.Context(), `UPDATE media SET keep = 1 WHERE id = ?`, kept); err != nil {
		t.Fatal(err)
	}

	approval := fmt.Sprintf("/api/v1/instances/%d/approval", id)
	wantCode(t, "approve", e.call("POST", approval, e.mom, nil), 200, "")
	retention := e.clk.Now().AddDate(0, 0, 30) // the household default
	if _, _, _, exp := e.mediaRow(gone); exp.String != db.Timestamp(retention) {
		t.Errorf("expires_at after approval = %v, want %s", exp, db.Timestamp(retention))
	}
	if _, _, _, exp := e.mediaRow(kept); exp.Valid {
		t.Errorf("kept media got an expiry: %v", exp)
	}
	if _, _, _, exp := e.mediaRow(auto); exp.String != db.Timestamp(retention) {
		t.Errorf("an instance approved on completion: expires_at %v", exp)
	}
	wantCode(t, "reverse the desk's approval", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/approval/reversal", desk), e.mom, nil), 200, "")
	if _, _, _, exp := e.mediaRow(auto); exp.Valid {
		t.Errorf("a reopened instance's media still expires: %v", exp)
	}

	e.clk.Advance(29 * 24 * time.Hour)
	if res := e.expire(e.local()); res.Expired != 0 {
		t.Errorf("expired %d items before retention ran out", res.Expired)
	}
	e.clk.Advance(2 * 24 * time.Hour)
	if res := e.expire(e.local()); res.Expired != 1 {
		t.Errorf("expired %d, want the approved instance's unkept photo", res.Expired)
	}
	if res := e.expire(e.local()); res.Expired != 0 || res.Failed != 0 {
		t.Errorf("a second run: %+v", res)
	}
	e.wantFiles(e.storageKey(gone), false)
	e.wantFiles(e.storageKey(kept), true)
	e.wantFiles(e.storageKey(auto), true)
	if _, _, status, _ := e.mediaRow(gone); status != "expired" {
		t.Errorf("status %s, want expired", status)
	}
	var hashes int
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT count(*) FROM media_hashes`).Scan(&hashes); err != nil || hashes != 3 {
		t.Errorf("media_hashes = %d, %v; the hashes outlive the files", hashes, err)
	}

	ev := e.evidence(id, e.sam)
	if p := ev[0].Photos[1]; !p.Expired || p.URL != "" || p.ThumbURL != "" {
		t.Errorf("expired photo: %+v", p)
	}
	if p := ev[0].Photos[0]; p.Expired || p.URL == "" {
		t.Errorf("kept photo: %+v", p)
	}
}

// A parent reopening an instance while retention deletes its photo can't leave the photo ready
// without its files: retention claims it first, and the reopen doesn't revive a claimed photo.
func TestRetentionClaimsBeforeDeleting(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 1})
	m := e.newPhoto(e.sam)
	wantCode(t, "complete", e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(m, "", shotToday)}}), 200, "")
	wantCode(t, "approve", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/approval", id), e.mom, nil), 200, "")
	e.clk.Advance(31 * 24 * time.Hour)
	e.mom = sessionCookie(t, e.do("POST", "/api/v1/auth/login", login(nil))) // the old session expired

	reopened := false
	store := &hookStore{Storage: e.local(), before: func(string) {
		if !reopened { // the reversal lands between the run's listing and its deletes
			reopened = true
			wantCode(t, "reverse the approval", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/approval/reversal", id), e.mom, nil), 200, "")
		}
	}}
	if res := e.expire(store); res.Expired != 1 {
		t.Errorf("expire: %+v", res)
	}
	if _, _, status, _ := e.mediaRow(m); status != "expired" {
		t.Errorf("status %s, want expired", status)
	}
	e.wantFiles(e.storageKey(m), false)
	if p := e.evidence(id, e.mom)[0].Photos[0]; !p.Expired || p.URL != "" {
		t.Errorf("the photo after the reopen: %+v", p)
	}
}

// A file that can't be deleted is skipped, counted and retried; it doesn't stop the others.
func TestRetentionSkipsUndeletableFiles(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 2})
	stuck, fine := e.newPhoto(e.sam), e.newPhoto(e.sam)
	wantCode(t, "complete", e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(stuck, "", shotToday), kidPhoto(fine, "", shotToday)}}), 200, "")
	wantCode(t, "approve", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/approval", id), e.mom, nil), 200, "")
	e.clk.Advance(31 * 24 * time.Hour)
	e.mom = sessionCookie(t, e.do("POST", "/api/v1/auth/login", login(nil))) // the old session expired

	key := e.storageKey(stuck)
	if res := e.expire(&hookStore{Storage: e.local(), fail: map[string]bool{key: true}}); res.Expired != 1 || res.Failed != 1 {
		t.Errorf("first run: %+v, want 1 expired and 1 failed", res)
	}
	if _, _, status, _ := e.mediaRow(stuck); status != "deleting" {
		t.Errorf("the stuck photo is %s, want deleting", status)
	}
	e.wantFiles(e.storageKey(fine), false)
	for _, p := range e.evidence(id, e.mom)[0].Photos {
		if !p.Expired {
			t.Errorf("photo %d isn't shown as expired", p.MediaID)
		}
	}
	if res := e.expire(e.local()); res.Expired != 1 || res.Failed != 0 {
		t.Errorf("the retry: %+v", res)
	}
	e.wantFiles(key, false)
	if _, _, status, _ := e.mediaRow(stuck); status != "expired" {
		t.Errorf("after the retry: %s", status)
	}
}

// Unkept evidence whose instance is gone (deleted with its kid) is deleted at the next run,
// resolved or not; kept photos stay.
func TestOrphanedEvidenceIsDeleted(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 2})
	m, kept := e.newPhoto(e.sam), e.newPhoto(e.sam)
	wantCode(t, "complete", e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(m, "", shotToday), kidPhoto(kept, "", shotToday)}}), 200, "")
	if _, err := e.store.SQL.ExecContext(t.Context(), `UPDATE media SET keep = 1 WHERE id = ?`, kept); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SQL.ExecContext(t.Context(), `DELETE FROM chore_instances WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if res := e.expire(e.local()); res.Expired != 1 {
		t.Fatalf("expire: %+v; want the unkept orphaned photo", res)
	}
	e.wantFiles(e.storageKey(m), false)
	e.wantFiles(e.storageKey(kept), true)
}

// An instance missed after a rejection resolves, and so does excusing it later: each starts the
// evidence's retention from then.
func TestMissAndExcuseStartRetention(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 1})
	m := e.newPhoto(e.sam)
	wantCode(t, "complete", e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(m, "", shotToday)}}), 200, "")
	wantCode(t, "reject", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/rejection", id), e.mom, map[string]any{"reason": "not_done"}), 200, "")
	quiet := slog.New(slog.DiscardHandler)
	bus := events.NewBus(e.store, jobs.NewRunner(e.store.Queries, e.clk, quiet), e.clk, quiet)
	if err := bus.InTx(t.Context(), func(tx *events.Tx) error {
		_, err := dayclose.MissInTx(t.Context(), tx, e.clk, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, exp := e.mediaRow(m); exp.String != db.Timestamp(e.clk.Now().AddDate(0, 0, 30)) {
		t.Errorf("expires_at after the miss = %v", exp)
	}
	e.clk.Advance(2 * 24 * time.Hour)
	wantCode(t, "excuse", e.call("POST", fmt.Sprintf("/api/v1/instances/%d/excusal", id), e.mom, nil), 200, "")
	if _, _, _, exp := e.mediaRow(m); exp.String != db.Timestamp(e.clk.Now().AddDate(0, 0, 30)) {
		t.Errorf("expires_at after the excuse = %v, want 30 days from the excuse", exp)
	}
}

func TestDiscardPendingPhoto(t *testing.T) {
	e := newEvidenceEnv(t)
	id := e.chore("Bed", e.samID, true, map[string]any{"photos": 1})
	path := func(mid int64) string { return fmt.Sprintf("/api/v1/media/photos/%d", mid) }

	// Fill the cap; the next upload is refused until a photo is discarded.
	first := e.uploaded(e.sam, photo(t, 80, 21))
	key := e.storageKey(first.ID)
	e.shot = 100
	for range media.MaxPendingUploads - 1 {
		e.newPhoto(e.sam)
	}
	e.shot++
	next := photo(t, 80, 20+e.shot)
	wantCode(t, "over the cap", e.upload(e.sam, next, "image/jpeg"), 429, "rate_limited")

	wantCode(t, "another kid", e.raw("DELETE", path(first.ID), e.sam2, nil, ""), 404, "media_not_found")
	wantCode(t, "a parent who isn't the uploader", e.raw("DELETE", path(first.ID), e.mom, nil, ""), 404, "media_not_found")
	wantCode(t, "unknown id", e.raw("DELETE", path(9999), e.sam, nil, ""), 404, "media_not_found")
	wantCode(t, "anonymous", e.raw("DELETE", path(first.ID), nil, nil, ""), 401, "unauthenticated")
	e.wantFiles(key, true)

	wantCode(t, "discard", e.raw("DELETE", path(first.ID), e.sam, nil, ""), 204, "")
	e.wantFiles(key, false)
	if _, _, status, _ := e.mediaRow(first.ID); status != "expired" {
		t.Errorf("discarded photo status %s, want expired", status)
	}
	wantCode(t, "repeated discard", e.raw("DELETE", path(first.ID), e.sam, nil, ""), 204, "")
	wantCode(t, "the slot is free", e.upload(e.sam, next, "image/jpeg"), 201, "")
	// The discarded picture's hash was dropped, so it isn't a duplicate (the cap is full again).
	wantCode(t, "same picture again", e.upload(e.sam, photo(t, 80, 21), "image/jpeg"), 429, "rate_limited")
	wantLocations(t, "attach a discarded photo", e.complete(id, e.sam,
		map[string]any{"photos": []any{kidPhoto(first.ID, "", shotToday)}}), "body.photos[0].mediaId")

	// An attached photo stays.
	mine := e.newPhotoAfterFree()
	wantCode(t, "complete", e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(mine, "", shotToday)}}), 200, "")
	wantCode(t, "discard an attached photo", e.raw("DELETE", path(mine), e.sam, nil, ""), 409, "media_attached")
	e.wantFiles(e.storageKey(mine), true)
}

// newPhotoAfterFree discards one of sam's pending photos to make room, then uploads a new one.
func (e *evidenceEnv) newPhotoAfterFree() int64 {
	e.t.Helper()
	var victim int64
	if err := e.store.SQL.QueryRowContext(e.t.Context(),
		`SELECT id FROM media WHERE uploaded_by = ? AND status = 'ready' ORDER BY id LIMIT 1`, e.samID).Scan(&victim); err != nil {
		e.t.Fatal(err)
	}
	wantCode(e.t, "make room", e.raw("DELETE", fmt.Sprintf("/api/v1/media/photos/%d", victim), e.sam, nil, ""), 204, "")
	return e.newPhoto(e.sam)
}

// A completion attaching a photo while its uploader discards it: one wins, and the data agrees.
func TestDiscardRacesAttach(t *testing.T) {
	for range 10 {
		e := newEvidenceEnv(t)
		id := e.chore("Bed", e.samID, true, map[string]any{"photos": 1})
		mid := e.newPhoto(e.sam)
		key := e.storageKey(mid)
		var completed, discarded *httptest.ResponseRecorder
		done := make(chan struct{}, 2)
		go func() {
			completed = e.complete(id, e.sam, map[string]any{"photos": []any{kidPhoto(mid, "", shotToday)}})
			done <- struct{}{}
		}()
		go func() {
			discarded = e.raw("DELETE", fmt.Sprintf("/api/v1/media/photos/%d", mid), e.sam, nil, "")
			done <- struct{}{}
		}()
		<-done
		<-done
		owner, _, status, _ := e.mediaRow(mid)
		if completed.Code == 200 {
			wantCode(t, "discard after attach", discarded, 409, "media_attached")
			if owner != "evidence" || status != "ready" {
				t.Errorf("attached photo is %s/%s", owner, status)
			}
			e.wantFiles(key, true)
			continue
		}
		wantCode(t, "discard won", discarded, 204, "")
		if owner != "pending" || status != "expired" {
			t.Errorf("discarded photo is %s/%s", owner, status)
		}
		e.wantFiles(key, false)
	}
}
