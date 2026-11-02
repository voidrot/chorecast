package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/media"
)

// mediaFilesPath is where signed media URLs point (under Prefix).
const mediaFilesPath = "/media/files"

// multipartOverhead is allowed on top of the photo size limit for the multipart framing.
const multipartOverhead = 64 << 10

func registerMedia(hapi huma.API, h *handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "uploadPhoto",
		Method:      http.MethodPost,
		Path:        "/media/photos",
		Summary:     "Upload a photo",
		Description: "A multipart form with the image in `file` (JPEG, PNG or WebP, recognized by its content, " +
			"not its declared type; HEIC isn't supported). The server applies the EXIF orientation, scales the photo " +
			"to at most 1600 px on its long edge, makes a 320 px thumbnail, and re-encodes both as WebP without any " +
			"metadata (EXIF, GPS). The photo is pending until a completion attaches it as evidence; " +
			fmt.Sprintf("one not attached within %d hours is deleted. ", int(media.PendingTTL.Hours())) +
			"A kid's photo that exactly matches earlier kid evidence is rejected with 409 `duplicate_media`, except " +
			"that a kid repeating their own upload that is still pending (an offline retry) gets that photo back. " +
			fmt.Sprintf("A kid may hold %d pending photos; more is 429 `rate_limited`. ", media.MaxPendingUploads) +
			"Images over 24 MP or 12,000 px on a " +
			"side, or that would need too much memory to decode, get 413; animated images get 415. Photos are processed " +
			"one at a time; when several uploads are already waiting the answer is 503 `media_busy`, and a caller with " +
			"two uploads in progress gets 429 `rate_limited`, both with Retry-After.",
		Tags:          []string{"media"},
		Security:      auth.Require(auth.ScopeChoresComplete),
		DefaultStatus: http.StatusCreated,
		// Huma sets no body limit or read timeout for multipart bodies, so set both.
		MaxBodyBytes:    h.cfg.Media.MaxPhotoBytes() + multipartOverhead,
		BodyReadTimeout: 2 * time.Minute,
		Errors: []int{http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
			http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable},
	}, h.uploadPhoto)
	huma.Register(hapi, huma.Operation{
		OperationID: "getMedia",
		Method:      http.MethodGet,
		Path:        "/media/{id}",
		Summary:     "Media details with fresh signed links",
		Description: "The links work without credentials for 10 minutes (`urlExpiresAt`); fetch the media again for new ones.",
		Tags:        []string{"media"},
		Security:    auth.Require(auth.ScopeChoresRead),
		Errors:      []int{http.StatusNotFound},
	}, h.getMedia)
	huma.Register(hapi, huma.Operation{
		OperationID: "getMediaFile",
		Method:      http.MethodGet,
		Path:        mediaFilesPath + "/{key}",
		Summary:     "A media file, through a signed link",
		Description: "Public: the signature in the link is the credential. A wrong or expired signature is 403 " +
			"`invalid_media_url`; media whose file was deleted (retention) is 410 `media_expired`.",
		Tags:     []string{"media"},
		Metadata: auth.Public(),
		Responses: map[string]*huma.Response{
			"200": {
				Description: "The file",
				Content:     map[string]*huma.MediaType{media.MimeWebP: {Schema: &huma.Schema{Type: "string", Format: "binary"}}},
			},
		},
		Errors: []int{http.StatusForbidden, http.StatusGone},
	}, h.getMediaFile)
}

type MediaThumbnail struct {
	Width  int64  `json:"width"`
	Height int64  `json:"height"`
	URL    string `json:"url" format:"uri" doc:"Signed link, valid until urlExpiresAt"`
}

type MediaItem struct {
	ID           int64          `json:"id"`
	Kind         string         `json:"kind" enum:"photo"`
	Mime         string         `json:"mime" example:"image/webp"`
	Bytes        int64          `json:"bytes"`
	Width        int64          `json:"width"`
	Height       int64          `json:"height"`
	URL          string         `json:"url" format:"uri" doc:"Signed link, valid until urlExpiresAt"`
	Thumbnail    MediaThumbnail `json:"thumbnail"`
	URLExpiresAt time.Time      `json:"urlExpiresAt"`
	CreatedAt    time.Time      `json:"createdAt"`
}

type MediaItemOutput struct {
	Body MediaItem
}

func (h *handlers) mediaItem(ctx context.Context, m sqlc.Medium) (MediaItem, error) {
	urls, err := h.signedURLs(ctx, m)
	if err != nil {
		return MediaItem{}, err
	}
	created, err := db.ParseTimestamp(m.CreatedAt)
	if err != nil {
		return MediaItem{}, err
	}
	return MediaItem{
		ID: m.ID, Kind: m.Kind, Mime: m.Mime, Bytes: m.Bytes, Width: m.Width, Height: m.Height,
		URL:          urls.URL,
		Thumbnail:    MediaThumbnail{Width: m.ThumbWidth.Int64, Height: m.ThumbHeight.Int64, URL: urls.ThumbURL},
		URLExpiresAt: urls.Expires, CreatedAt: created,
	}, nil
}

// signedURLs signs links to m and makes them absolute against the request's base URL. Callers
// must have checked access first. It is an evidence.Signer.
func (h *handlers) signedURLs(ctx context.Context, m sqlc.Medium) (media.SignedURLs, error) {
	urls, err := h.media.URLs(ctx, m)
	if err != nil {
		return media.SignedURLs{}, err
	}
	base := auth.RequestInfoFrom(ctx).BaseURL(h.cfg.PublicURL)
	abs := func(u string) string {
		if strings.HasPrefix(u, "/") {
			return base + u
		}
		return u
	}
	urls.URL, urls.ThumbURL = abs(urls.URL), abs(urls.ThumbURL)
	return urls, nil
}

type MediaUploadForm struct {
	// No contentType tag: Huma would check the part's declared type, which isn't trusted. The
	// service checks the magic bytes instead.
	File huma.FormFile `form:"file" required:"true" doc:"The image: JPEG, PNG or WebP"`
}

type UploadPhotoInput struct {
	RawBody huma.MultipartFormFiles[MediaUploadForm]
}

func (h *handlers) uploadPhoto(ctx context.Context, in *UploadPhotoInput) (*MediaItemOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	f := in.RawBody.Data().File
	defer func() { _ = f.Close() }()
	limit := h.cfg.Media.MaxPhotoBytes()
	if f.Size > limit {
		return nil, media.TooLarge(limit)
	}
	m, _, err := h.media.UploadPhoto(ctx, p, f, limit) // a kid's retry gets its earlier upload, also 201
	if err != nil {
		return nil, err
	}
	item, err := h.mediaItem(ctx, m)
	if err != nil {
		return nil, err
	}
	return &MediaItemOutput{Body: item}, nil
}

type GetMediaInput struct {
	ID int64 `path:"id"`
}

func (h *handlers) getMedia(ctx context.Context, in *GetMediaInput) (*MediaItemOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	m, err := h.media.Get(ctx, p, in.ID)
	if err != nil {
		return nil, err
	}
	item, err := h.mediaItem(ctx, m)
	if err != nil {
		return nil, err
	}
	return &MediaItemOutput{Body: item}, nil
}

type GetMediaFileInput struct {
	Key string `path:"key" maxLength:"40"`
	Exp string `query:"exp" doc:"Expiry, Unix seconds (part of the signed link)"`
	Sig string `query:"sig" doc:"Signature (part of the signed link)"`
}

type MediaFileOutput struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	NoSniff      string `header:"X-Content-Type-Options"`
	Body         []byte
}

func (h *handlers) getMediaFile(ctx context.Context, in *GetMediaFileInput) (*MediaFileOutput, error) {
	f, err := h.media.Open(ctx, in.Key, in.Exp, in.Sig)
	if err != nil {
		return nil, err
	}
	maxAge := max(0, int(f.Expires.Sub(h.clock.Now()).Seconds()))
	return &MediaFileOutput{
		ContentType:  f.Mime,
		CacheControl: fmt.Sprintf("private, max-age=%d", maxAge),
		NoSniff:      "nosniff",
		Body:         f.Data,
	}, nil
}
