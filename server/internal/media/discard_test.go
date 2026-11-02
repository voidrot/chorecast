package media_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/media/mediatest"
)

// failingStore fails every Delete while fail is set.
type failingStore struct {
	media.Storage
	fail bool
}

func (s *failingStore) Delete(ctx context.Context, key string) error {
	if s.fail {
		return errors.New("disk on fire")
	}
	return s.Storage.Delete(ctx, key)
}

func TestDiscardKeepsRowWhenFilesCannotBeDeleted(t *testing.T) {
	signer, clk, d := newSigner(t)
	store := &failingStore{Storage: media.NewLocal(t.TempDir(), "/files", signer)}
	svc := media.New(d, clk, store, signer)
	u, err := d.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Sam", Role: "child"})
	if err != nil {
		t.Fatal(err)
	}
	kid := &auth.Principal{Kind: auth.KindKid, UserID: u.ID}
	pending := func() int64 {
		n, err := d.CountPendingUploads(t.Context(), sqlc.CountPendingUploadsParams{
			UploadedBy: sql.NullInt64{Int64: u.ID, Valid: true},
			Now:        sql.NullString{String: db.Timestamp(clk.Now()), Valid: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	m, _, err := svc.UploadPhoto(t.Context(), kid, bytes.NewReader(mediatest.JPEG(t, mediatest.Halves(64, 48))), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pending() != 1 {
		t.Fatalf("pending = %d before the discard", pending())
	}

	store.fail = true
	if err := svc.Discard(t.Context(), kid, m.ID); err != nil {
		t.Fatalf("discard with undeletable files: %v", err)
	}
	if got, _ := d.GetMedia(t.Context(), m.ID); got.Status != media.StatusDeleting {
		t.Errorf("status %s, want deleting", got.Status)
	}
	if pending() != 0 {
		t.Errorf("the slot isn't free: pending = %d", pending())
	}

	store.fail = false
	res, err := media.Expire(t.Context(), d, clk, store, slog.New(slog.DiscardHandler))
	if err != nil || res.Expired != 1 || res.Failed != 0 {
		t.Fatalf("expire: %+v, %v", res, err)
	}
	if got, _ := d.GetMedia(t.Context(), m.ID); got.Status != media.StatusExpired {
		t.Errorf("status %s, want expired", got.Status)
	}
	if rc, err := store.Get(t.Context(), m.StorageKey); err == nil {
		_ = rc.Close()
		t.Error("the file is still there")
	}
}

func TestDiscardAttachedIsConflict(t *testing.T) {
	signer, clk, d := newSigner(t)
	svc := media.New(d, clk, media.NewLocal(t.TempDir(), "/files", signer), signer)
	u, _ := d.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Sam", Role: "child"})
	kid := &auth.Principal{Kind: auth.KindKid, UserID: u.ID}
	m, _, err := svc.UploadPhoto(t.Context(), kid, bytes.NewReader(mediatest.JPEG(t, mediatest.Halves(64, 48))), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Attached and no longer ready, as if a completion won between the read and the claim.
	if _, err := d.SQL.ExecContext(t.Context(),
		`UPDATE media SET owner_kind = 'evidence', owner_id = 1, expires_at = NULL WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	err = svc.Discard(t.Context(), kid, m.ID)
	var pe *apierr.Problem
	if !errors.As(err, &pe) || pe.GetStatus() != http.StatusConflict {
		t.Fatalf("discard of an attached photo: %v, want 409", err)
	}
}
