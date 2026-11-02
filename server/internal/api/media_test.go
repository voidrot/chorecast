package api_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/vpx/webp"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/media/mediatest"
)

type mediaJSON struct {
	ID            int64
	Kind, Mime    string
	Bytes         int64
	Width, Height int64
	URL           string
	Thumbnail     struct {
		Width, Height int64
		URL           string
	}
	URLExpiresAt time.Time
}

// mediaEnv is a set-up household with a session for mom and device tokens for kids sam and
// sam-2.
type mediaEnv struct {
	*setupServer
	dataDir   string
	mom       *http.Cookie
	sam, sam2 string
}

func newMediaEnv(t *testing.T, cfg config.Config) *mediaEnv {
	t.Helper()
	cfg.DataDir = t.TempDir()
	s := newAuthServerWith(t, cfg)
	e := &mediaEnv{setupServer: s, dataDir: cfg.DataDir}
	e.mom = sessionCookie(t, s.do("POST", "/api/v1/auth/login", login(nil)))
	_, e.sam = pairDevice(t, s, pair(t, s, e.mom, "sam").Code)
	_, e.sam2 = pairDevice(t, s, pair(t, s, e.mom, "sam-2").Code)
	return e
}

// upload posts data as the multipart "file" part, declared as partType. auth is a bearer
// token or a session cookie.
func (e *mediaEnv) upload(auth any, data []byte, partType string) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="photo.jpg"`)
	h.Set("Content-Type", partType)
	part, err := w.CreatePart(h)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = w.Close()
	return e.raw("POST", "/api/v1/media/photos", auth, &body, w.FormDataContentType())
}

func (e *mediaEnv) raw(method, path string, auth any, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	e.t.Helper()
	if body == nil {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequestWithContext(e.t.Context(), method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	switch a := auth.(type) {
	case string:
		req.Header.Set("Authorization", "Bearer "+a)
	case *http.Cookie:
		req.AddCookie(a)
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

// fetch GETs a signed media URL without credentials.
func (e *mediaEnv) fetch(rawURL string) *httptest.ResponseRecorder {
	e.t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.raw("GET", u.RequestURI(), nil, nil, "")
}

func (e *mediaEnv) uploaded(auth any, data []byte) mediaJSON {
	e.t.Helper()
	rec := e.upload(auth, data, "image/jpeg")
	wantCode(e.t, "upload", rec, 201, "")
	return decode[mediaJSON](e.t, rec)
}

func photo(t *testing.T, w, h int) []byte { return mediatest.JPEG(t, mediatest.Halves(w, h)) }

func TestUploadPhoto(t *testing.T) {
	e := newMediaEnv(t, config.Config{})
	src := mediatest.WithExif(photo(t, 2400, 1200), 6) // GPS data, needs a 90° turn
	m := e.uploaded(e.sam, src)
	// Turned upright (1200×2400), then scaled to 1600 on the long edge.
	if m.Kind != "photo" || m.Mime != "image/webp" || m.Width != 800 || m.Height != 1600 ||
		m.Thumbnail.Width != 160 || m.Thumbnail.Height != 320 {
		t.Fatalf("media = %+v", m)
	}
	if !strings.HasPrefix(m.URL, "http://example.com/api/v1/media/files/") ||
		!m.URLExpiresAt.Equal(e.clk.Now().Add(10*time.Minute)) {
		t.Errorf("url = %s, expires %s", m.URL, m.URLExpiresAt)
	}

	for name, link := range map[string]string{"photo": m.URL, "thumb": m.Thumbnail.URL} {
		rec := e.fetch(link)
		wantCode(t, "fetch "+name, rec, 200, "")
		hdr := rec.Header()
		if hdr.Get("Content-Type") != "image/webp" || hdr.Get("Cache-Control") != "private, max-age=600" ||
			hdr.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s headers = %v", name, hdr)
		}
		body := rec.Body.Bytes()
		if bytes.Contains(body, []byte(mediatest.GPSMarker)) || bytes.Contains(body, []byte("Exif")) {
			t.Errorf("%s still carries EXIF/GPS", name)
		}
		cfg, err := webp.DecodeConfig(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "photo" && (cfg.Width != 800 || cfg.Height != 1600) {
			t.Errorf("stored photo is %dx%d", cfg.Width, cfg.Height)
		}
	}

	// The stored row and files.
	var key, mime string
	var bytesN, owner int64
	var ownerKind string
	var uploadedBy int64
	if err := e.store.SQL.QueryRowContext(t.Context(),
		`SELECT storage_key, mime, bytes, owner_kind, coalesce(owner_id, 0), uploaded_by FROM media WHERE id = ?`, m.ID,
	).Scan(&key, &mime, &bytesN, &ownerKind, &owner, &uploadedBy); err != nil {
		t.Fatal(err)
	}
	if ownerKind != "pending" || owner != 0 || bytesN != m.Bytes || uploadedBy == 0 {
		t.Errorf("row: owner %s/%d, bytes %d, uploaded_by %d", ownerKind, owner, bytesN, uploadedBy)
	}
	for _, k := range []string{key, media.ThumbKey(key)} {
		b, err := os.ReadFile(filepath.Join(e.dataDir, "media", k[0:2], k[2:4], k)) //nolint:gosec // test path
		if err != nil || bytes.Contains(b, []byte(mediatest.GPSMarker)) {
			t.Errorf("file %s: %v", k, err)
		}
	}
}

func TestUploadPhotoDuplicates(t *testing.T) {
	e := newMediaEnv(t, config.Config{})
	first := photo(t, 64, 48)
	m := e.uploaded(e.sam, first)

	wantCode(t, "same kid, same photo", e.upload(e.sam, first, "image/jpeg"), 409, "duplicate_media")
	wantCode(t, "same photo with other metadata", e.upload(e.sam, mediatest.WithExif(first, 1), "image/jpeg"), 409, "duplicate_media")
	wantCode(t, "another kid, same photo", e.upload(e.sam2, first, "image/jpeg"), 409, "duplicate_media")
	// Parents' uploads aren't evidence: neither checked nor recorded.
	e.uploaded(e.mom, first)
	fromMom := photo(t, 64, 50)
	e.uploaded(e.mom, fromMom)
	e.uploaded(e.sam, fromMom)
	e.uploaded(e.sam, photo(t, 64, 49)) // a different photo is fine

	// The hash outlives the media.
	if _, err := e.store.SQL.ExecContext(t.Context(), `DELETE FROM media WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "reuse after deletion", e.upload(e.sam, first, "image/jpeg"), 409, "duplicate_media")
	var n int
	if err := e.store.SQL.QueryRowContext(t.Context(), `SELECT count(*) FROM media_hashes`).Scan(&n); err != nil || n != 3 {
		t.Errorf("media_hashes rows = %d, %v; want 3 (kid uploads only)", n, err)
	}
}

func TestUploadPhotoValidation(t *testing.T) {
	e := newMediaEnv(t, config.Config{Media: config.MediaConfig{MaxPhotoMB: 1}})

	// Recognized by content, whatever the part declares.
	var p bytes.Buffer
	if err := png.Encode(&p, mediatest.Halves(30, 20)); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "png declared as jpeg", e.upload(e.sam, p.Bytes(), "image/jpeg"), 201, "")
	wantCode(t, "jpeg declared as octet-stream", e.upload(e.sam, photo(t, 30, 21), "application/octet-stream"), 201, "")

	html := []byte("<html><script>alert(1)</script></html>")
	wantCode(t, "html declared as jpeg", e.upload(e.sam, html, "image/jpeg"), 415, "unsupported_media_type")
	wantCode(t, "riff, not webp", e.upload(e.sam, []byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00"), "image/webp"), 415, "unsupported_media_type")
	heic := append([]byte{0, 0, 0, 0x18}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
	wantCode(t, "heic", e.upload(e.sam, heic, "image/heic"), 415, "unsupported_media_type")
	jpg := photo(t, 30, 22)
	wantCode(t, "truncated jpeg", e.upload(e.sam, jpg[:len(jpg)/2], "image/jpeg"), 422, "invalid_image")
	wantCode(t, "animated webp", e.upload(e.sam, mediatest.AnimatedWebP(t), "image/webp"), 415, "unsupported_media_type")
	wantCode(t, "thin bomb", e.upload(e.sam, mediatest.PNGHeader(40_000_000, 1, 8, 0, 0), "image/png"), 413, "image_too_large")
	wantCode(t, "16-bit png", e.upload(e.sam, mediatest.PNGHeader(4800, 4800, 16, 6, 0), "image/png"), 413, "image_too_complex")

	// Size: the file part over the limit, and a body far over it.
	big := make([]byte, 1<<20+1)
	_, _ = rand.Read(big)
	copy(big, []byte{0xFF, 0xD8, 0xFF})
	wantCode(t, "oversize file", e.upload(e.sam, big, "image/jpeg"), 413, "payload_too_large")
	wantCode(t, "oversize body", e.upload(e.sam, make([]byte, 3<<20), "image/jpeg"), 413, "payload_too_large")

	wantCode(t, "no file", e.raw("POST", "/api/v1/media/photos", e.sam, bytes.NewBufferString("--x--\r\n"),
		"multipart/form-data; boundary=x"), 422, "validation_failed")
	wantCode(t, "anonymous", e.upload(nil, jpg, "image/jpeg"), 401, "unauthenticated")

	display, _, err := auth.NewStore(e.store.Queries, e.clk).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDisplay})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "display", e.upload(display, jpg, "image/jpeg"), 403, "insufficient_scope")
	_, shared := pairDevice(t, e.setupServer, pair(t, e.setupServer, e.mom, "").Code)
	wantCode(t, "shared device without a kid session", e.upload(shared, jpg, "image/jpeg"), 403, "insufficient_scope")
}

func TestMediaFileLinks(t *testing.T) {
	e := newMediaEnv(t, config.Config{})
	m := e.uploaded(e.sam, photo(t, 40, 30))
	u, _ := url.Parse(m.URL)
	q := u.Query()
	key := strings.TrimPrefix(u.Path, "/api/v1/media/files/")

	link := func(key, exp, sig string) string {
		return "/api/v1/media/files/" + key + "?" + url.Values{"exp": {exp}, "sig": {sig}}.Encode()
	}
	sig := []byte(q.Get("sig"))
	sig[3] ^= 1
	thumbKey := strings.TrimPrefix(mustURL(t, m.Thumbnail.URL).Path, "/api/v1/media/files/")
	for name, l := range map[string]string{
		"tampered signature":        link(key, q.Get("exp"), string(sig)),
		"extended expiry":           link(key, fmt.Sprint(e.clk.Now().Add(time.Hour).Unix()), q.Get("sig")),
		"photo signature for thumb": link(thumbKey, q.Get("exp"), q.Get("sig")),
		"no signature":              "/api/v1/media/files/" + key,
		"not a key":                 link("..%2F..%2Fchorecast.db", q.Get("exp"), q.Get("sig")),
	} {
		wantCode(t, name, e.fetch(l), 403, "invalid_media_url")
	}

	e.clk.Advance(5 * time.Minute)
	if rec := e.fetch(m.URL); rec.Code != 200 || rec.Header().Get("Cache-Control") != "private, max-age=300" {
		t.Errorf("halfway: %d %v", rec.Code, rec.Header())
	}
	e.clk.Advance(5 * time.Minute)
	wantCode(t, "expired link", e.fetch(m.URL), 403, "invalid_media_url")

	// A fresh link for a deleted file reports the media as expired, not a server error.
	fresh := decode[mediaJSON](t, e.do("GET", fmt.Sprintf("/api/v1/media/%d", m.ID), nil, e.mom))
	wantCode(t, "fresh link", e.fetch(fresh.URL), 200, "")
	if err := os.Remove(filepath.Join(e.dataDir, "media", key[0:2], key[2:4], key)); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "missing file", e.fetch(fresh.URL), 410, "media_expired")
	if _, err := e.store.SQL.ExecContext(t.Context(), `UPDATE media SET status = 'expired' WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "expired media, file still there", e.fetch(fresh.Thumbnail.URL), 410, "media_expired")
	if _, err := e.store.SQL.ExecContext(t.Context(), `DELETE FROM media WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "deleted media", e.fetch(fresh.Thumbnail.URL), 410, "media_expired")
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMediaScoping(t *testing.T) {
	e := newMediaEnv(t, config.Config{})
	samPhoto := e.uploaded(e.sam, photo(t, 40, 30))
	momPhoto := e.uploaded(e.mom, photo(t, 40, 31))
	path := func(m mediaJSON) string { return fmt.Sprintf("/api/v1/media/%d", m.ID) }

	wantCode(t, "uploader", e.raw("GET", path(samPhoto), e.sam, nil, ""), 200, "")
	wantCode(t, "parent", e.raw("GET", path(samPhoto), e.mom, nil, ""), 200, "")
	wantCode(t, "another kid", e.raw("GET", path(samPhoto), e.sam2, nil, ""), 404, "media_not_found")
	wantCode(t, "kid, parent's upload", e.raw("GET", path(momPhoto), e.sam, nil, ""), 404, "media_not_found")
	display, _, err := auth.NewStore(e.store.Queries, e.clk).Issue(t.Context(), auth.IssueParams{Kind: auth.TokenDisplay})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "display", e.raw("GET", path(samPhoto), display, nil, ""), 404, "media_not_found")
	wantCode(t, "anonymous", e.raw("GET", path(samPhoto), nil, nil, ""), 401, "unauthenticated")
	wantCode(t, "unknown id", e.raw("GET", "/api/v1/media/999", e.mom, nil, ""), 404, "media_not_found")
}
