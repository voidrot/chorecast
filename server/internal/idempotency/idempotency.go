// Package idempotency makes POST requests safe to retry. A client that sends an
// Idempotency-Key header gets the first response replayed for any retry with the same key and
// body, instead of the action running twice; the Android app's offline outbox relies on this.
//
// Keys are scoped to the caller's token, kept for 24 hours, and only apply to authenticated
// JSON POSTs. Server errors (5xx) are not stored, so the client can retry them for real.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

const (
	Header         = "Idempotency-Key"
	ReplayedHeader = "Idempotent-Replayed"
	// TTL is how long a key is remembered.
	TTL = 24 * time.Hour
	// abandonAfter treats an in-progress key as abandoned (the server crashed mid-request).
	abandonAfter = 5 * time.Minute
	maxKeyLen    = 255
	maxBodyBytes = 1 << 20
)

// Middleware must run after auth.Middleware (it needs the principal).
func Middleware(api huma.API, q *sqlc.Queries, clk clock.Clock) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		key := ctx.Header(Header)
		p, authed := auth.PrincipalFrom(ctx.Context())
		if key == "" || ctx.Method() != http.MethodPost || !authed || !isJSON(ctx.Header("Content-Type")) {
			next(ctx)
			return
		}
		if !validKey(key) {
			_ = huma.WriteErr(api, ctx, http.StatusBadRequest, "Idempotency-Key must be 1-255 visible ASCII characters",
				apierr.WithCode(apierr.CodeInvalidIdempotency))
			return
		}

		body, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), maxBodyBytes+1))
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusBadRequest, "could not read request body")
			return
		}
		if len(body) > maxBodyBytes {
			_ = huma.WriteErr(api, ctx, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		hash := requestHash(ctx, body)
		h := handler{api: api, q: q, clk: clk, tokenID: p.TokenID, key: key}

		reserved, err := h.reserve(ctx.Context(), hash)
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "idempotency store unavailable", err)
			return
		}
		if !reserved {
			h.replayOrReject(ctx, hash)
			return
		}

		rec := &recorder{base: ctx, body: bytes.NewReader(body)}
		next(rec)
		h.finish(ctx.Context(), rec)
	}
}

type handler struct {
	api     huma.API
	q       *sqlc.Queries
	clk     clock.Clock
	tokenID int64
	key     string
}

// reserve claims the key for this request. Expired or abandoned keys are cleared first.
func (h handler) reserve(ctx context.Context, hash []byte) (bool, error) {
	now := h.clk.Now()
	existing, err := h.q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{TokenID: h.tokenID, Key: h.key})
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	default:
		created, perr := db.ParseTimestamp(existing.CreatedAt)
		stale := perr != nil || now.Sub(created) >= TTL || (!existing.Status.Valid && now.Sub(created) >= abandonAfter)
		if !stale {
			return false, nil
		}
		if err := h.q.DeleteIdempotencyKey(ctx, sqlc.DeleteIdempotencyKeyParams{TokenID: h.tokenID, Key: h.key}); err != nil {
			return false, err
		}
	}
	_, err = h.q.ReserveIdempotencyKey(ctx, sqlc.ReserveIdempotencyKeyParams{
		TokenID: h.tokenID, Key: h.key, RequestHash: hash, CreatedAt: db.Timestamp(now),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // lost a race to a concurrent request with the same key
	}
	return err == nil, err
}

func (h handler) replayOrReject(ctx huma.Context, hash []byte) {
	row, err := h.q.GetIdempotencyKey(ctx.Context(), sqlc.GetIdempotencyKeyParams{TokenID: h.tokenID, Key: h.key})
	switch {
	case err != nil:
		_ = huma.WriteErr(h.api, ctx, http.StatusInternalServerError, "idempotency store unavailable", err)
	case !bytes.Equal(row.RequestHash, hash):
		_ = huma.WriteErr(h.api, ctx, http.StatusUnprocessableEntity,
			"Idempotency-Key was already used for a different request", apierr.WithCode(apierr.CodeIdempotencyReuse))
	case !row.Status.Valid:
		_ = huma.WriteErr(h.api, ctx, http.StatusConflict,
			"a request with this Idempotency-Key is still in progress; retry shortly", apierr.WithCode(apierr.CodeIdempotencyInFlight))
	default:
		if row.ContentType.Valid {
			ctx.SetHeader("Content-Type", row.ContentType.String)
		}
		ctx.SetHeader(ReplayedHeader, "true")
		ctx.SetStatus(int(row.Status.Int64))
		_, _ = ctx.BodyWriter().Write(row.Body)
	}
}

// finish stores the response, or forgets the key after a server error so a retry runs again.
// It uses a context that survives the request ending.
func (h handler) finish(ctx context.Context, rec *recorder) {
	ctx = context.WithoutCancel(ctx)
	ids := sqlc.DeleteIdempotencyKeyParams{TokenID: h.tokenID, Key: h.key}
	status := rec.Status()
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 500 {
		_ = h.q.DeleteIdempotencyKey(ctx, ids)
		return
	}
	_ = h.q.CompleteIdempotencyKey(ctx, sqlc.CompleteIdempotencyKeyParams{
		Status:      sql.NullInt64{Int64: int64(status), Valid: true},
		ContentType: sql.NullString{String: rec.contentType, Valid: rec.contentType != ""},
		Body:        rec.buf.Bytes(),
		TokenID:     h.tokenID,
		Key:         h.key,
	})
}

// Purge deletes keys older than TTL. Called by the daily maintenance job.
func Purge(ctx context.Context, q *sqlc.Queries, now time.Time) (int64, error) {
	return q.DeleteIdempotencyKeysBefore(ctx, db.Timestamp(now.Add(-TTL)))
}

// AddHeaderParam documents the Idempotency-Key header on every POST operation that the
// middleware covers (not multipart uploads). Register it as an OpenAPI OnAddOperation hook so
// generated clients can send it.
func AddHeaderParam(_ *huma.OpenAPI, op *huma.Operation) {
	if op.Method != http.MethodPost {
		return
	}
	if op.RequestBody != nil && op.RequestBody.Content["multipart/form-data"] != nil {
		return
	}
	maxLen := maxKeyLen
	op.Parameters = append(op.Parameters, &huma.Param{
		Name:        Header,
		In:          "header",
		Description: "Optional. Retries with the same key and body replay the first response instead of repeating the action (24 h).",
		Schema:      &huma.Schema{Type: "string", MinLength: new(1), MaxLength: &maxLen},
	})
}

func requestHash(ctx huma.Context, body []byte) []byte {
	h := sha256.New()
	u := ctx.URL()
	_, _ = io.WriteString(h, ctx.Method()+" "+u.RequestURI()+"\n")
	_, _ = h.Write(body)
	return h.Sum(nil)
}

func validKey(k string) bool {
	if len(k) == 0 || len(k) > maxKeyLen {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

func isJSON(ct string) bool {
	mt, _, _ := strings.Cut(ct, ";")
	mt = strings.TrimSpace(strings.ToLower(mt))
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// base is huma.Context under another name, so embedding it doesn't create a field named
// Context that would hide the interface's Context() method.
type base = huma.Context

// recorder wraps the request context to replay the buffered request body and capture the
// response status, content type and body.
type recorder struct {
	base
	body        io.Reader
	contentType string
	buf         bytes.Buffer
}

func (r *recorder) BodyReader() io.Reader { return r.body }

func (r *recorder) SetHeader(name, value string) {
	if strings.EqualFold(name, "Content-Type") {
		r.contentType = value
	}
	r.base.SetHeader(name, value)
}

func (r *recorder) BodyWriter() io.Writer { return io.MultiWriter(r.base.BodyWriter(), &r.buf) }
