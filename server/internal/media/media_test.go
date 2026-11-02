package media_test

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/media/mediatest"
)

// UploadPhoto relies on this to reject a duplicate that raced past its early check: the second
// insert of a hash returns no row.
func TestRecordMediaHashReportsAnExistingHash(t *testing.T) {
	_, _, store := newSigner(t)
	p := sqlc.RecordMediaHashParams{Sha256: make([]byte, 32), Dhash: 7, CreatedAt: "2026-10-31T12:00:00Z"}
	if _, err := store.RecordMediaHash(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordMediaHash(t.Context(), p); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("second insert: err = %v, want sql.ErrNoRows", err)
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

// Uploads wait for the single processing slot without reading their bodies, at most
// MaxQueuedUploads of them; the next one is turned away at once with 503 media_busy.
func TestUploadQueue(t *testing.T) {
	signer, clk, store := newSigner(t)
	svc := media.New(store, clk, media.NewLocal(t.TempDir(), "/files", signer), signer)
	u, err := store.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Mom", Role: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	parent := &auth.Principal{Kind: auth.KindParent, UserID: u.ID}
	release := make(chan struct{})
	var reads atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 1+media.MaxQueuedUploads)
	for i := range 1 + media.MaxQueuedUploads {
		data := mediatest.JPEG(t, mediatest.Halves(16, 10+i))
		wg.Go(func() {
			_, err := svc.UploadPhoto(t.Context(), parent, &blockingReader{release, &reads, bytes.NewReader(data)}, 1<<20)
			errs <- err
		})
	}
	// Wait until the first upload holds the slot (it has started reading) and the rest queue.
	deadline := time.Now().Add(5 * time.Second)            //nolint:forbidigo // real-time wait for goroutines
	for reads.Load() == 0 && time.Now().Before(deadline) { //nolint:forbidigo // as above
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the others reach the queue
	if n := reads.Load(); n != 1 {
		t.Fatalf("%d uploads read their body; only the one holding the slot should", n)
	}

	_, err = svc.UploadPhoto(t.Context(), parent, bytes.NewReader(nil), 1<<20)
	var p *apierr.Problem
	if !errors.As(err, &p) || p.Status != 503 || p.Code != apierr.CodeMediaBusy {
		t.Fatalf("over the queue: err = %v, want 503 media_busy", err)
	}
	var h huma.HeadersError
	if !errors.As(err, &h) || h.GetHeaders().Get("Retry-After") == "" {
		t.Error("503 media_busy should carry Retry-After")
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
