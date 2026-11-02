// Package media stores uploaded photos: it validates and re-encodes them (photo.go), keeps the
// files in a Storage (storage.go), records their hashes so a kid can't reuse earlier evidence,
// and serves them through short-lived signed URLs (signer.go).
// Spec: docs/features/approvals-evidence.md (Fresh-evidence rules, Media pipeline).
package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// URLTTL is how long a signed media URL stays valid.
const URLTTL = 10 * time.Minute

// Owner kinds (media.owner_kind). Uploads start as OwnerPending, with no owner id, until they
// are attached to evidence, a rejection, a chore or equipment.
const (
	OwnerPending = "pending"
	KindPhoto    = "photo"
	MimeWebP     = "image/webp"
)

var (
	errNotFound    = apierr.New(http.StatusNotFound, apierr.CodeMediaNotFound, "no such media")
	errExpired     = apierr.New(http.StatusGone, apierr.CodeMediaExpired, "this media has expired and was deleted")
	errInvalidURL  = apierr.New(http.StatusForbidden, apierr.CodeInvalidMediaURL, "the media link is invalid or has expired")
	errDuplicate   = apierr.New(http.StatusConflict, apierr.CodeDuplicateMedia, "this photo was already used as evidence; take a new one")
	errUnsupported = apierr.New(http.StatusUnsupportedMediaType, apierr.CodeUnsupportedMedia, "upload a JPEG, PNG or WebP photo")
	errHEIC        = apierr.New(http.StatusUnsupportedMediaType, apierr.CodeUnsupportedMedia,
		"HEIC and AVIF photos aren't supported; upload a JPEG, PNG or WebP photo")
	errTooManyPixels = apierr.New(http.StatusRequestEntityTooLarge, apierr.CodeImageTooLarge,
		fmt.Sprintf("the image is larger than %d megapixels or %d pixels on a side", MaxPixels/1_000_000, MaxSide))
	errTooComplex = apierr.New(http.StatusRequestEntityTooLarge, apierr.CodeImageTooComplex,
		"the image needs too much memory to process; save it as an ordinary JPEG and try again")
	errAnimated = apierr.New(http.StatusUnsupportedMediaType, apierr.CodeUnsupportedMedia,
		"animated images aren't supported; upload a still photo")
	errInvalidImage = apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidImage, "the image is damaged or can't be read")
)

// Service uploads, scopes and serves media.
type Service struct {
	db     *db.DB
	clock  clock.Clock
	store  Storage
	signer *Signer // verifies the local storage's signed URLs
	// sem bounds concurrent photo processing, so a Raspberry Pi isn't asked to hold several
	// decoded full-size photos at once. queue bounds the uploads holding or waiting for it.
	sem   chan struct{}
	queue chan struct{}

	mu       sync.Mutex
	inFlight map[callerKey]int // uploads holding or waiting for the slot, per caller
}

// MaxQueuedUploads is how many uploads may wait for the processing slot; more get 503
// media_busy with Retry-After. MaxUploadsPerCaller is how many of those places one caller
// (a parent or a kid, whatever device they use) may hold, so a single device sending many
// uploads at once can't keep everyone else out; over it the answer is 429 rate_limited.
const (
	MaxQueuedUploads    = 4
	MaxUploadsPerCaller = 2
)

type callerKey struct {
	kind auth.Kind
	id   int64
}

func keyFor(p *auth.Principal) callerKey {
	if p.UserID != 0 {
		return callerKey{p.Kind, p.UserID}
	}
	return callerKey{p.Kind, -p.TokenID}
}

// enter takes one of p's in-flight places, or reports that p holds them all.
func (s *Service) enter(p *auth.Principal) (leave func(), ok bool) {
	k := keyFor(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[k] >= MaxUploadsPerCaller {
		return nil, false
	}
	s.inFlight[k]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.inFlight[k]--; s.inFlight[k] == 0 {
			delete(s.inFlight, k)
		}
	}, true
}

// New returns the media service. It doesn't touch d or store until a request needs them.
// signer verifies the URLs Open serves; it should be the one store signs with.
func New(d *db.DB, c clock.Clock, store Storage, signer *Signer) *Service {
	return &Service{db: d, clock: c, store: store, signer: signer,
		sem: make(chan struct{}, 1), queue: make(chan struct{}, 1+MaxQueuedUploads), inFlight: map[callerKey]int{}}
}

// NewKey returns a new random storage key (32 hex digits).
func NewKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// UploadPhoto reads a photo of at most limit bytes from r, processes it and stores it as pending
// media uploaded by p. It reads r only once it holds the processing slot, so waiting uploads
// don't hold their photos in memory; when MaxQueuedUploads are already waiting it fails at once
// with 503 media_busy. A kid's photo whose pixels exactly match earlier kid evidence in the
// household is rejected (409 duplicate_media) before it is encoded, and its hash is recorded so
// later reuse is caught, even after the media is deleted. Parents' uploads are neither checked
// nor recorded (fresh-evidence rule 5).
func (s *Service) UploadPhoto(ctx context.Context, p *auth.Principal, r io.Reader, limit int64) (sqlc.Medium, error) {
	photo, err := s.process(ctx, p, r, limit)
	if err != nil {
		return sqlc.Medium{}, err
	}
	key, err := NewKey()
	if err != nil {
		return sqlc.Medium{}, err
	}
	thumbKey := ThumbKey(key)
	if err := s.store.Put(ctx, key, bytes.NewReader(photo.WebP)); err != nil {
		return sqlc.Medium{}, err
	}
	if err := s.store.Put(ctx, thumbKey, bytes.NewReader(photo.Thumb)); err != nil {
		s.removeFiles(key, thumbKey)
		return sqlc.Medium{}, err
	}
	m, err := s.insert(ctx, p, p.Kind == auth.KindKid, key, thumbKey, photo)
	if err != nil {
		s.removeFiles(key, thumbKey)
		return sqlc.Medium{}, err
	}
	return m, nil
}

// TooLarge is the 413 for an upload over limit bytes.
func TooLarge(limit int64) error {
	return apierr.New(http.StatusRequestEntityTooLarge, apierr.CodePayloadTooLarge,
		fmt.Sprintf("photos can be at most %d MB", limit>>20))
}

var errBusy = huma.ErrorWithHeaders(
	apierr.New(http.StatusServiceUnavailable, apierr.CodeMediaBusy, "the server is busy processing other photos; try again shortly"),
	http.Header{"Retry-After": {"5"}})

var errTooManyOwn = huma.ErrorWithHeaders(
	apierr.New(http.StatusTooManyRequests, apierr.CodeRateLimited, "wait for your other photo uploads to finish"),
	http.Header{"Retry-After": {"5"}})

// process reads, decodes and encodes the photo while holding the processing slot.
func (s *Service) process(ctx context.Context, p *auth.Principal, r io.Reader, limit int64) (*Photo, error) {
	leave, ok := s.enter(p)
	if !ok {
		return nil, errTooManyOwn
	}
	defer leave()
	select {
	case s.queue <- struct{}{}:
		defer func() { <-s.queue }()
	default:
		return nil, errBusy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, TooLarge(limit)
	}
	d, err := DecodePhoto(ctx, data)
	if err != nil {
		return nil, photoError(err)
	}
	if p.Kind == auth.KindKid {
		// Answer before paying for the encode; the insert in the transaction is what guarantees it.
		dup, err := s.db.MediaHashExists(ctx, d.SHA256[:])
		if err != nil {
			return nil, err
		}
		if dup {
			return nil, errDuplicate
		}
	}
	photo, err := d.Encode(ctx)
	if err != nil {
		return nil, photoError(err)
	}
	return photo, nil
}

func photoError(err error) error {
	switch {
	case errors.Is(err, ErrHEIC):
		return errHEIC
	case errors.Is(err, ErrAnimated):
		return errAnimated
	case errors.Is(err, ErrUnsupportedFormat):
		return errUnsupported
	case errors.Is(err, ErrTooManyPixels):
		return errTooManyPixels
	case errors.Is(err, ErrTooComplex):
		return errTooComplex
	case errors.Is(err, ErrInvalidImage):
		return errInvalidImage
	default:
		return err
	}
}

func (s *Service) insert(ctx context.Context, p *auth.Principal, kid bool, key, thumbKey string, photo *Photo) (sqlc.Medium, error) {
	now := db.Timestamp(s.clock.Now())
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return sqlc.Medium{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.db.WithTx(tx)
	dhash := int64(photo.DHash) //nolint:gosec // stored bit-for-bit; SQLite has no unsigned integers
	m, err := q.CreateMedia(ctx, sqlc.CreateMediaParams{
		UploadedBy: sql.NullInt64{Int64: p.UserID, Valid: p.UserID != 0},
		StorageKey: key, ThumbKey: sql.NullString{String: thumbKey, Valid: true},
		Kind: KindPhoto, Mime: MimeWebP, Bytes: int64(len(photo.WebP)),
		Width: int64(photo.Width), Height: int64(photo.Height),
		ThumbWidth:  sql.NullInt64{Int64: int64(photo.ThumbWidth), Valid: true},
		ThumbHeight: sql.NullInt64{Int64: int64(photo.ThumbHeight), Valid: true},
		Sha256:      photo.SHA256[:], Dhash: dhash, CreatedAt: now,
	})
	if err != nil {
		return sqlc.Medium{}, fmt.Errorf("create media: %w", err)
	}
	if kid {
		_, err := q.RecordMediaHash(ctx, sqlc.RecordMediaHashParams{
			Sha256: photo.SHA256[:], Dhash: dhash,
			KidID:     sql.NullInt64{Int64: p.UserID, Valid: true},
			MediaID:   sql.NullInt64{Int64: m.ID, Valid: true},
			CreatedAt: now,
		})
		if errors.Is(err, sql.ErrNoRows) { // the hash was already there
			return sqlc.Medium{}, errDuplicate
		}
		if err != nil {
			return sqlc.Medium{}, fmt.Errorf("record media hash: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return sqlc.Medium{}, err
	}
	return m, nil
}

// removeFiles cleans up after a failed upload. It runs without the request's context, which may
// be what was cancelled.
func (s *Service) removeFiles(keys ...string) {
	for _, k := range keys {
		_ = s.store.Delete(context.Background(), k)
	}
}

// CanAccess reports whether p may see m. Parents see all media. A kid sees pending media they
// uploaded. Media attached to an owner is visible to parents only until its owner kind defines
// more (evidence: the instance's kid, #27).
func CanAccess(p *auth.Principal, m sqlc.Medium) bool {
	switch {
	case p.Kind == auth.KindParent:
		return true
	case m.OwnerKind == OwnerPending:
		return p.Kind == auth.KindKid && m.UploadedBy.Valid && m.UploadedBy.Int64 == p.UserID
	default:
		return false
	}
}

// Get returns media id if p may see it, else 404 media_not_found (also for media that exists
// but is out of p's scope, so ids can't be probed).
func (s *Service) Get(ctx context.Context, p *auth.Principal, id int64) (sqlc.Medium, error) {
	m, err := s.db.GetMedia(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.Medium{}, errNotFound
	}
	if err != nil {
		return sqlc.Medium{}, err
	}
	if !CanAccess(p, m) {
		return sqlc.Medium{}, errNotFound
	}
	return m, nil
}

// SignedURLs are fresh links to a media file and its thumbnail, valid until Expires.
type SignedURLs struct {
	URL      string
	ThumbURL string // empty when the media has no thumbnail
	Expires  time.Time
}

// URLs signs links to m's file and thumbnail. They may be relative to the server's base URL.
// Callers must have checked access (Get or CanAccess) first.
func (s *Service) URLs(ctx context.Context, m sqlc.Medium) (SignedURLs, error) {
	exp := s.clock.Now().Add(URLTTL).Truncate(time.Second)
	out := SignedURLs{Expires: exp}
	var err error
	if out.URL, err = s.store.SignedURL(ctx, m.StorageKey, exp); err != nil {
		return SignedURLs{}, err
	}
	if m.ThumbKey.Valid {
		if out.ThumbURL, err = s.store.SignedURL(ctx, m.ThumbKey.String, exp); err != nil {
			return SignedURLs{}, err
		}
	}
	return out, nil
}

// File is a media file opened through a signed URL.
type File struct {
	Mime    string
	Data    []byte
	Expires time.Time // when the URL stops working
}

// Open serves the file under key for a signed URL (exp and sig as in the query string). A bad
// or expired signature is 403 invalid_media_url; media that was deleted, or whose file is gone,
// is 410 media_expired.
func (s *Service) Open(ctx context.Context, key, exp, sig string) (File, error) {
	if !ValidKey(key) {
		return File{}, errInvalidURL
	}
	ok, err := s.signer.Verify(ctx, key, exp, sig)
	if err != nil {
		return File{}, err
	}
	if !ok {
		return File{}, errInvalidURL
	}
	m, err := s.db.GetMediaByKey(ctx, key)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && m.Status != "ready") {
		return File{}, errExpired
	}
	if err != nil {
		return File{}, err
	}
	rc, err := s.store.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return File{}, errExpired
	}
	if err != nil {
		return File{}, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return File{}, err
	}
	sec, _ := strconv.ParseInt(exp, 10, 64) // Verify accepted it
	return File{Mime: m.Mime, Data: data, Expires: time.Unix(sec, 0)}, nil
}
