package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/media/mediatest"
)

// The busy error reaches the client as a 503 problem with its code and Retry-After.
func TestBusyResponse(t *testing.T) {
	_, api := humatest.New(t)
	huma.Get(api, "/busy", func(context.Context, *struct{}) (*struct{}, error) { return nil, errBusy })
	rec := api.Get("/busy")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
	}
	var body struct{ Code string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "media_busy" {
		t.Errorf("body = %s (%v)", rec.Body, err)
	}
}

// blockingReader blocks its first Read until release is closed, and counts reads.
type blockingReader struct {
	release <-chan struct{}
	reads   *atomic.Int32
	data    io.Reader
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	<-r.release
	return r.data.Read(p)
}

// waitFor polls cond in real time, failing the test after 10 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 10_000 {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type queueEnv struct {
	svc *Service
	db  *db.DB
}

func newQueueEnv(t *testing.T) *queueEnv {
	t.Helper()
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clk := clock.NewFake(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	signer := NewSigner(store.Queries, clk)
	return &queueEnv{svc: New(store, clk, NewLocal(t.TempDir(), "/files", signer), signer), db: store}
}

func (e *queueEnv) parent(t *testing.T) *auth.Principal {
	t.Helper()
	u, err := e.db.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Parent", Role: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	return &auth.Principal{Kind: auth.KindParent, UserID: u.ID}
}

// blockedUploads starts uploads by the given callers whose bodies block until release closes.
func (e *queueEnv) blockedUploads(t *testing.T, callers []*auth.Principal, reads *atomic.Int32) (release func()) {
	t.Helper()
	ch := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, len(callers))
	for i, p := range callers {
		data := mediatest.JPEG(t, mediatest.Halves(16, 10+i))
		wg.Go(func() {
			_, _, err := e.svc.UploadPhoto(t.Context(), p, &blockingReader{ch, reads, bytes.NewReader(data)}, 1<<20)
			errs <- err
		})
	}
	return func() {
		close(ch)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("queued upload: %v", err)
			}
		}
	}
}

func wantProblem(t *testing.T, what string, err error, status int, code string) {
	t.Helper()
	var p *apierr.Problem
	if !errors.As(err, &p) || p.Status != status || p.Code != code {
		t.Fatalf("%s: err = %v, want %d %s", what, err, status, code)
	}
	var h huma.HeadersError
	if !errors.As(err, &h) || h.GetHeaders().Get("Retry-After") == "" {
		t.Errorf("%s: no Retry-After", what)
	}
}

// Uploads wait for the single processing slot without reading their bodies, at most
// MaxQueuedUploads of them; the next one is turned away at once with 503 media_busy.
func TestUploadQueue(t *testing.T) {
	e := newQueueEnv(t)
	var callers []*auth.Principal
	for range 1 + MaxQueuedUploads {
		callers = append(callers, e.parent(t))
	}
	var reads atomic.Int32
	release := e.blockedUploads(t, callers, &reads)
	waitFor(t, "the queue to fill", func() bool { return len(e.svc.queue) == cap(e.svc.queue) })
	waitFor(t, "the slot holder to read", func() bool { return reads.Load() > 0 })

	_, _, err := e.svc.UploadPhoto(t.Context(), e.parent(t), bytes.NewReader(nil), 1<<20)
	wantProblem(t, "over the queue", err, http.StatusServiceUnavailable, apierr.CodeMediaBusy)
	time.Sleep(20 * time.Millisecond) // a waiter reading now would be a bug; give it the chance
	if n := reads.Load(); n != 1 {
		t.Errorf("%d uploads read their body; only the one holding the slot should", n)
	}
	release()
	if n := len(e.svc.inFlight); n != 0 {
		t.Errorf("%d callers still counted in flight", n)
	}
}

// One caller holds at most MaxUploadsPerCaller places, so they can't fill the queue alone.
func TestUploadQueuePerCaller(t *testing.T) {
	e := newQueueEnv(t)
	mom := e.parent(t)
	var callers []*auth.Principal
	for range MaxUploadsPerCaller {
		callers = append(callers, mom)
	}
	var reads atomic.Int32
	release := e.blockedUploads(t, callers, &reads)
	waitFor(t, "mom's uploads to queue", func() bool { return len(e.svc.queue) == MaxUploadsPerCaller })

	_, _, err := e.svc.UploadPhoto(t.Context(), mom, bytes.NewReader(nil), 1<<20)
	wantProblem(t, "mom's third upload", err, http.StatusTooManyRequests, apierr.CodeRateLimited)
	// Someone else still gets a place.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := e.svc.UploadPhoto(cancelled, e.parent(t), bytes.NewReader(nil), 1<<20); !errors.Is(err, context.Canceled) {
		t.Errorf("another parent's upload: err = %v, want it queued (then cancelled)", err)
	}
	release()
}
