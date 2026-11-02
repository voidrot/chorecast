package media_test

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/media"
)

func newSigner(t *testing.T) (*media.Signer, *clock.Fake, *db.DB) {
	t.Helper()
	store, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clk := clock.NewFake(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	return media.NewSigner(store.Queries, clk), clk, store
}

func TestLocalStorage(t *testing.T) {
	signer, _, _ := newSigner(t)
	dir := t.TempDir()
	l := media.NewLocal(dir, "/api/v1/media/files", signer)
	key, err := media.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Put(t.Context(), key, strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if err := l.Put(t.Context(), key, strings.NewReader("second")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "media", key[0:2], key[2:4], key)
	if b, err := os.ReadFile(path); err != nil || string(b) != "second" { //nolint:gosec // test path
		t.Fatalf("file = %q, %v", b, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
	rc, err := l.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "second" {
		t.Errorf("Get = %q", b)
	}
	if err := l.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(t.Context(), key); err != nil {
		t.Errorf("deleting a missing file: %v", err)
	}
	if _, err := l.Get(t.Context(), key); !errors.Is(err, media.ErrNotFound) {
		t.Errorf("Get after delete: %v", err)
	}
	for _, bad := range []string{"../../etc/passwd", "", "ABCDEF0123456789abcdef0123456789", key + "/x", key[:30]} {
		if err := l.Put(t.Context(), bad, strings.NewReader("x")); err == nil {
			t.Errorf("Put accepted key %q", bad)
		}
		if _, err := l.Get(t.Context(), bad); err == nil {
			t.Errorf("Get accepted key %q", bad)
		}
	}
}

func TestSignedURLs(t *testing.T) {
	signer, clk, _ := newSigner(t)
	l := media.NewLocal(t.TempDir(), "/api/v1/media/files", signer)
	key, _ := media.NewKey()
	exp := clk.Now().Add(media.URLTTL)
	raw, err := l.SignedURL(t.Context(), key, exp)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/api/v1/media/files/"+key {
		t.Errorf("path = %q", u.Path)
	}
	q := u.Query()
	verify := func(key, exp, sig string) bool {
		t.Helper()
		ok, err := signer.Verify(t.Context(), key, exp, sig)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !verify(key, q.Get("exp"), q.Get("sig")) {
		t.Fatal("a fresh signature should verify")
	}
	other, _ := media.NewKey()
	sig := q.Get("sig")
	tampered := []byte(sig)
	tampered[0] ^= 1
	for name, args := range map[string][3]string{
		"other key":      {other, q.Get("exp"), sig},
		"thumb key":      {media.ThumbKey(key), q.Get("exp"), sig},
		"later expiry":   {key, "9999999999", sig},
		"tampered sig":   {key, q.Get("exp"), string(tampered)},
		"empty sig":      {key, q.Get("exp"), ""},
		"padded expiry":  {key, "0" + q.Get("exp"), sig},
		"garbage expiry": {key, "soon", sig},
	} {
		if verify(args[0], args[1], args[2]) {
			t.Errorf("%s: verified", name)
		}
	}
	clk.Advance(media.URLTTL)
	if verify(key, q.Get("exp"), q.Get("sig")) {
		t.Error("an expired signature should not verify")
	}
}

func TestSignerKeyPersists(t *testing.T) {
	signer, clk, store := newSigner(t)
	key, _ := media.NewKey()
	exp := clk.Now().Add(time.Minute)
	sig, err := signer.Sign(t.Context(), key, exp)
	if err != nil {
		t.Fatal(err)
	}
	// A new signer (a restarted server) loads the same secret, so earlier URLs still verify.
	again := media.NewSigner(store.Queries, clk)
	ok, err := again.Verify(t.Context(), key, strconv.FormatInt(exp.Unix(), 10), sig)
	if err != nil || !ok {
		t.Errorf("verify after restart = %v, %v", ok, err)
	}
}

func TestSignerRejectsLongLivedLinks(t *testing.T) {
	signer, clk, _ := newSigner(t)
	key, _ := media.NewKey()
	for exp, want := range map[time.Duration]bool{media.URLTTL: true, media.URLTTL + time.Second: false, 24 * time.Hour: false} {
		e := clk.Now().Add(exp)
		sig, err := signer.Sign(t.Context(), key, e)
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := signer.Verify(t.Context(), key, strconv.FormatInt(e.Unix(), 10), sig); ok != want {
			t.Errorf("validly signed link expiring in %v: verified = %v, want %v", exp, ok, want)
		}
	}
}

func TestRotateURLSecret(t *testing.T) {
	signer, clk, store := newSigner(t)
	key, _ := media.NewKey()
	exp := clk.Now().Add(time.Minute)
	sig, _ := signer.Sign(t.Context(), key, exp)
	if err := media.RotateURLSecret(t.Context(), store.Queries, clk); err != nil {
		t.Fatal(err)
	}
	e := strconv.FormatInt(exp.Unix(), 10)
	// The running signer keeps its key until restart; a restarted one uses the new secret.
	if ok, _ := signer.Verify(t.Context(), key, e, sig); !ok {
		t.Error("the running signer should keep its loaded key")
	}
	restarted := media.NewSigner(store.Queries, clk)
	if ok, _ := restarted.Verify(t.Context(), key, e, sig); ok {
		t.Error("a link signed before rotation verified after a restart")
	}
	fresh, _ := restarted.Sign(t.Context(), key, exp)
	if ok, _ := restarted.Verify(t.Context(), key, e, fresh); !ok {
		t.Error("a link signed after rotation should verify")
	}
}
