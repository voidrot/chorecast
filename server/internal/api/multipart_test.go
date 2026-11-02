package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

type multipartTestInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true"`
	}]
}

// Whatever the operation answers, the files its multipart body was spooled to are deleted.
func TestRemoveMultipartFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	r := chi.NewRouter()
	hapi := humachi.New(r, huma.DefaultConfig("t", "1"))
	// Like auth.Middleware, swap the context first: that copies the request.
	hapi.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		next(huma.WithValue(ctx, struct{}{}, 1))
	}, removeMultipartFiles)
	for path, status := range map[string]int{"/ok": 204, "/busy": 503, "/limit": 429, "/large": 413} {
		huma.Register(hapi, huma.Operation{Method: http.MethodPost, Path: path, MaxBodyBytes: 1 << 20, DefaultStatus: 204},
			func(context.Context, *multipartTestInput) (*struct{}, error) {
				if status == 204 {
					return nil, nil
				}
				return nil, huma.NewError(status, "no")
			})
	}
	for path, want := range map[string]int{"/ok": 204, "/busy": 503, "/limit": 429, "/large": 413} {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file", "photo.jpg")
		_, _ = part.Write(make([]byte, 64<<10)) // over the 8 KB kept in memory
		_ = w.Close()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s: status %d, want %d", path, rec.Code, want)
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("%s: temp files left: %v", path, left)
		}
	}
}
