package api

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
)

// removeMultipartFiles deletes the temp files a multipart request body was spooled to, whatever
// the operation answered. Huma parses the form (spooling parts over 8 KB to os.TempDir) on a
// copy of the request that chi middleware made, and net/http only cleans up the form of the
// request it created, so without this every upload, rejected or not, leaves its file behind.
// It must be the last middleware: huma.WithContext (used by auth) copies the request, and the
// form is parsed on the copy the operation receives.
func removeMultipartFiles(ctx huma.Context, next func(huma.Context)) {
	defer func() {
		if r, _ := humachi.Unwrap(ctx); r != nil && r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	next(ctx)
}
