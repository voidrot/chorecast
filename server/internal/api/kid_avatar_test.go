package api_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/media/mediatest"
)

type avatarKid struct {
	Slug                 string
	Avatar               string
	AvatarPhotoURL       string
	AvatarPhotoExpiresAt *time.Time
}

func (e *mediaEnv) setAvatar(auth any, kid string, data []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.uploadTo("PUT", "/api/v1/kids/"+kid+"/avatar/photo", auth, data, "image/jpeg")
}

func (e *mediaEnv) removeAvatar(auth any, kid string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.raw("DELETE", "/api/v1/kids/"+kid+"/avatar/photo", auth, nil, "")
}

func (e *mediaEnv) getKid(auth any, path string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.raw("GET", path, auth, nil, "")
}

func (e *mediaEnv) mediaRow(id int64) (status string, expires *string, key string) {
	e.t.Helper()
	if err := e.store.SQL.QueryRowContext(e.t.Context(),
		`SELECT status, expires_at, storage_key FROM media WHERE id = ?`, id).Scan(&status, &expires, &key); err != nil {
		e.t.Fatal(err)
	}
	return
}

func TestKidAvatarPhoto(t *testing.T) {
	e := newMediaEnv(t, config.Config{})
	if k := decode[avatarKid](t, e.getKid(e.mom, "/api/v1/kids/sam")); k.AvatarPhotoURL != "" || k.Avatar == "" {
		t.Fatalf("before upload: %+v, want the bundled avatar only", k)
	}

	rec := e.setAvatar(e.mom, "sam", mediatest.WithExif(photo(t, 600, 400), 1))
	wantCode(t, "set avatar", rec, 200, "")
	first := decode[avatarKid](t, rec)
	if first.AvatarPhotoURL == "" || first.Avatar == "" || first.AvatarPhotoExpiresAt == nil ||
		!first.AvatarPhotoExpiresAt.Equal(e.clk.Now().Add(media.URLTTL)) {
		t.Fatalf("response = %+v", first)
	}
	// The link works without credentials, and no EXIF/GPS is left.
	file := e.fetch(first.AvatarPhotoURL)
	wantCode(t, "fetch avatar", file, 200, "")
	if b := file.Body.Bytes(); len(b) == 0 || bytes.Contains(b, []byte(mediatest.GPSMarker)) {
		t.Errorf("avatar body: %d bytes", len(b))
	}
	// The kid reads the photo link through their own profile.
	if got := decode[avatarKid](t, e.getKid(e.sam, "/api/v1/kids/sam")); got.AvatarPhotoURL == "" {
		t.Error("the kid's profile read has no photo link")
	}
	var id int64
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT id FROM media WHERE owner_kind = 'kid_avatar'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	st, exp, key := e.mediaRow(id)
	if st != "ready" || exp != nil {
		t.Errorf("avatar media: status %s, expires %v; want ready, never", st, exp)
	}
	oldFile := filepath.Join(e.dataDir, "media", key[0:2], key[2:4], key)

	// Replacing releases the old photo for the retention job.
	wantCode(t, "replace avatar", e.setAvatar(e.mom, "sam", photo(t, 640, 480)), 200, "")
	if st, _, _ := e.mediaRow(id); st != "deleting" {
		t.Errorf("replaced media status = %s, want deleting", st)
	}
	wantCode(t, "old link after replace", e.fetch(first.AvatarPhotoURL), 410, "media_expired")
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("old file before retention: %v", err)
	}

	// Years later retention deletes the old photo's files and leaves the current photo alone.
	e.clk.Advance(5 * 365 * 24 * time.Hour)
	e.mom = sessionCookie(t, e.do("POST", "/api/v1/auth/login", login(nil))) // the old session lapsed
	res, err := media.Expire(t.Context(), e.store, e.clk, media.NewLocal(e.dataDir, "", nil), slog.New(slog.DiscardHandler))
	if err != nil || res.Expired != 1 {
		t.Fatalf("Expire = %+v, %v; want only the released photo", res, err)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("old file after retention: %v, want gone", err)
	}
	var curID int64
	if err := e.store.SQL.QueryRowContext(t.Context(),
		`SELECT id FROM media WHERE owner_kind = 'kid_avatar' AND status = 'ready'`).Scan(&curID); err != nil {
		t.Fatalf("the current photo vanished: %v", err)
	}
	_, _, curKey := e.mediaRow(curID)
	if _, err := os.Stat(filepath.Join(e.dataDir, "media", curKey[0:2], curKey[2:4], curKey)); err != nil {
		t.Errorf("current file after retention: %v", err)
	}

	// Archiving keeps it.
	wantCode(t, "archive", e.raw("POST", "/api/v1/kids/sam/archive", e.mom, nil, ""), 200, "")
	list := decode[struct{ Items []avatarKid }](t, e.getKid(e.mom, "/api/v1/kids?archived=true"))
	var archived *avatarKid
	for i := range list.Items {
		if list.Items[i].Slug == "sam" {
			archived = &list.Items[i]
		}
	}
	if archived == nil || archived.AvatarPhotoURL == "" {
		t.Errorf("archived kid in the list: %+v", archived)
	}

	// Removing goes back to the bundled avatar; removing again is harmless.
	rec = e.removeAvatar(e.mom, "sam")
	wantCode(t, "remove avatar", rec, 200, "")
	if k := decode[avatarKid](t, rec); k.AvatarPhotoURL != "" || k.Avatar == "" {
		t.Errorf("after remove: %+v", k)
	}
	wantCode(t, "remove again", e.removeAvatar(e.mom, "sam"), 200, "")
	if st, _, _ := e.mediaRow(curID); st != "deleting" {
		t.Errorf("removed media status = %s, want deleting", st)
	}
}

func TestKidAvatarPhotoRules(t *testing.T) {
	e := newMediaEnv(t, config.Config{})
	img := photo(t, 64, 48)
	// A kid's device, even for the kid's own profile, can't set or remove it.
	wantCode(t, "kid sets own", e.setAvatar(e.sam, "sam", img), http.StatusForbidden, "insufficient_scope")
	wantCode(t, "kid sets other", e.setAvatar(e.sam, "sam-2", img), http.StatusForbidden, "insufficient_scope")
	wantCode(t, "kid removes", e.removeAvatar(e.sam, "sam"), http.StatusForbidden, "insufficient_scope")
	wantCode(t, "anonymous", e.setAvatar(nil, "sam", img), http.StatusUnauthorized, "unauthenticated")
	// Bad input is rejected like any upload.
	wantCode(t, "not an image", e.setAvatar(e.mom, "sam", []byte("hello")), 415, "unsupported_media_type")
	wantCode(t, "unknown kid", e.setAvatar(e.mom, "nobody", img), 404, "kid_not_found")
	var n int
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT count(*) FROM media`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("media rows after refused uploads = %d, %v", n, err)
	}
	// Parents' uploads are exempt from the fresh-evidence rules: the same picture for two kids is fine,
	// and nothing is hashed.
	wantCode(t, "sam", e.setAvatar(e.mom, "sam", img), 200, "")
	wantCode(t, "sam-2, same picture", e.setAvatar(e.mom, "sam-2", img), 200, "")
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT count(*) FROM media_hashes`).Scan(&n); err != nil || n != 0 {
		t.Errorf("media_hashes rows = %d, %v; parent avatars aren't recorded", n, err)
	}
}
