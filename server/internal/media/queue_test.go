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

// Uploads wait for the single processing slot without reading their bodies, at most
// MaxQueuedUploads of them; the next one is turned away at once with 503 media_busy.
func TestUploadQueue(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clk := clock.NewFake(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	signer := NewSigner(store.Queries, clk)
	svc := New(store, clk, NewLocal(t.TempDir(), "/files", signer), signer)
	u, err := store.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Mom", Role: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	parent := &auth.Principal{Kind: auth.KindParent, UserID: u.ID}

	release := make(chan struct{})
	var reads atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 1+MaxQueuedUploads)
	for i := range 1 + MaxQueuedUploads {
		data := mediatest.JPEG(t, mediatest.Halves(16, 10+i))
		wg.Go(func() {
			_, err := svc.UploadPhoto(t.Context(), parent, &blockingReader{release, &reads, bytes.NewReader(data)}, 1<<20)
			errs <- err
		})
	}
	waitFor(t, "the queue to fill", func() bool { return len(svc.queue) == cap(svc.queue) })
	waitFor(t, "the slot holder to read", func() bool { return reads.Load() > 0 })

	_, err = svc.UploadPhoto(t.Context(), parent, bytes.NewReader(nil), 1<<20)
	var p *apierr.Problem
	if !errors.As(err, &p) || p.Status != http.StatusServiceUnavailable || p.Code != apierr.CodeMediaBusy {
		t.Fatalf("over the queue: err = %v, want 503 media_busy", err)
	}
	time.Sleep(20 * time.Millisecond) // a waiter reading now would be a bug; give it the chance
	if n := reads.Load(); n != 1 {
		t.Errorf("%d uploads read their body; only the one holding the slot should", n)
	}

	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("queued upload: %v", err)
		}
	}
}
