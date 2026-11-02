package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/media"
)

func registerKids(hapi huma.API, h *handlers) {
	manage := auth.Require(auth.ScopeHouseholdManage)
	huma.Register(hapi, huma.Operation{
		OperationID: "listKids",
		Method:      http.MethodGet,
		Path:        "/kids",
		Summary:     "Kid profiles",
		Description: "Archived kids are included (last) with `archived=true`, which needs household:manage.",
		Tags:        []string{"kids"},
		Security:    auth.Require(auth.ScopeHouseholdRead),
	}, h.listKids)
	huma.Register(hapi, huma.Operation{
		OperationID: "getKid",
		Method:      http.MethodGet,
		Path:        "/kids/{kid}",
		Summary:     "A kid profile (by id or slug)",
		Tags:        []string{"kids"},
		Security:    auth.Require(auth.ScopeHouseholdRead),
		Errors:      []int{http.StatusNotFound},
	}, h.getKid)
	huma.Register(hapi, huma.Operation{
		OperationID:   "createKid",
		Method:        http.MethodPost,
		Path:          "/kids",
		Summary:       "Add a kid",
		Tags:          []string{"kids"},
		Security:      manage,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, h.createKid)
	huma.Register(hapi, huma.Operation{
		OperationID: "updateKid",
		Method:      http.MethodPut,
		Path:        "/kids/{kid}",
		Summary:     "Edit a kid's profile",
		Description: "Replaces the profile: optional fields left out are cleared. The slug stays unless one is given.",
		Tags:        []string{"kids"},
		Security:    manage,
		Errors:      []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, h.updateKid)
	huma.Register(hapi, huma.Operation{
		OperationID:   "setKidPin",
		Method:        http.MethodPut,
		Path:          "/kids/{kid}/pin",
		Summary:       "Set or clear a kid's PIN",
		Description:   "An empty `pin` clears it. Whether it's asked for is the profile's `pinRequired`.",
		Tags:          []string{"kids"},
		Security:      manage,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, h.setKidPIN)
	huma.Register(hapi, huma.Operation{
		OperationID: "setKidAvatarPhoto",
		Method:      http.MethodPut,
		Path:        "/kids/{kid}/avatar/photo",
		Summary:     "Set a kid's photo avatar",
		Description: "A multipart form with the image in `file`, processed like any photo upload (same size and " +
			"pixel limits, EXIF and GPS stripped). It replaces any earlier photo, which is deleted, and is shown " +
			"instead of the bundled avatar until removed. It never expires. The kid-evidence rules don't apply.",
		Tags:            []string{"kids"},
		Security:        manage,
		MaxBodyBytes:    h.cfg.Media.MaxPhotoBytes() + multipartOverhead,
		BodyReadTimeout: 2 * time.Minute,
		Errors: []int{http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
			http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable},
	}, h.setKidAvatarPhoto)
	huma.Register(hapi, huma.Operation{
		OperationID: "removeKidAvatarPhoto",
		Method:      http.MethodDelete,
		Path:        "/kids/{kid}/avatar/photo",
		Summary:     "Remove a kid's photo avatar",
		Description: "The kid goes back to the bundled avatar. Removing when there is no photo does nothing.",
		Tags:        []string{"kids"},
		Security:    manage,
		Errors:      []int{http.StatusNotFound},
	}, h.removeKidAvatarPhoto)
	huma.Register(hapi, huma.Operation{
		OperationID: "archiveKid",
		Method:      http.MethodPost,
		Path:        "/kids/{kid}/archive",
		Summary:     "Archive a kid",
		Description: "Hides the kid, revokes their devices and stops scheduling; history is kept. Unarchive restores it.",
		Tags:        []string{"kids"},
		Security:    manage,
		Errors:      []int{http.StatusNotFound},
	}, h.archiveKid)
	huma.Register(hapi, huma.Operation{
		OperationID: "unarchiveKid",
		Method:      http.MethodPost,
		Path:        "/kids/{kid}/unarchive",
		Summary:     "Unarchive a kid (devices must be paired again)",
		Tags:        []string{"kids"},
		Security:    manage,
		Errors:      []int{http.StatusNotFound},
	}, h.unarchiveKid)
}

type ListKidsInput struct {
	Archived bool `query:"archived" doc:"Include archived kids (needs household:manage)"`
}

type KidsOutput struct {
	Body struct {
		Items []kids.Kid `json:"items" nullable:"false"`
	}
}

func (h *handlers) listKids(ctx context.Context, in *ListKidsInput) (*KidsOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	if in.Archived && !p.Has(auth.ScopeHouseholdManage) {
		return nil, apierr.New(http.StatusForbidden, apierr.CodeInsufficientScope, "archived kids are visible to parents only")
	}
	items, err := h.kids.List(ctx, in.Archived)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i], err = h.withAvatarPhoto(ctx, items[i]); err != nil {
			return nil, err
		}
	}
	out := &KidsOutput{}
	out.Body.Items = items
	return out, nil
}

type KidRefInput struct {
	Kid string `path:"kid" doc:"Kid id or slug"`
}

type KidOutput struct {
	Body kids.Kid
}

func (h *handlers) getKid(ctx context.Context, in *KidRefInput) (*KidOutput, error) {
	k, err := h.kids.Get(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	p, _ := auth.PrincipalFrom(ctx)
	if k.ArchivedAt != nil && !p.Has(auth.ScopeHouseholdManage) {
		return nil, apierr.New(http.StatusNotFound, apierr.CodeKidNotFound, "no such kid")
	}
	return h.kidOutput(ctx, k)
}

type CreateKidInput struct {
	Body kids.KidInput
}

func (h *handlers) createKid(ctx context.Context, in *CreateKidInput) (*KidOutput, error) {
	k, err := h.kids.Create(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return h.kidOutput(ctx, k)
}

type UpdateKidInput struct {
	Kid  string `path:"kid"`
	Body kids.KidInput
}

func (h *handlers) updateKid(ctx context.Context, in *UpdateKidInput) (*KidOutput, error) {
	k, err := h.kids.Update(ctx, in.Kid, in.Body)
	if err != nil {
		return nil, err
	}
	return h.kidOutput(ctx, k)
}

type SetKidPINInput struct {
	Kid  string `path:"kid"`
	Body struct {
		PIN string `json:"pin" maxLength:"8" doc:"4-8 digits; empty clears the PIN"`
	}
}

func (h *handlers) setKidPIN(ctx context.Context, in *SetKidPINInput) (*struct{}, error) {
	return nil, h.kids.SetPIN(ctx, in.Kid, in.Body.PIN)
}

func (h *handlers) archiveKid(ctx context.Context, in *KidRefInput) (*KidOutput, error) {
	k, err := h.kids.Archive(ctx, caller(ctx), in.Kid)
	if err != nil {
		return nil, err
	}
	return h.kidOutput(ctx, k)
}

func (h *handlers) unarchiveKid(ctx context.Context, in *KidRefInput) (*KidOutput, error) {
	k, err := h.kids.Unarchive(ctx, caller(ctx), in.Kid)
	if err != nil {
		return nil, err
	}
	return h.kidOutput(ctx, k)
}

func (h *handlers) kidOutput(ctx context.Context, k kids.Kid) (*KidOutput, error) {
	k, err := h.withAvatarPhoto(ctx, k)
	if err != nil {
		return nil, err
	}
	return &KidOutput{Body: k}, nil
}

// withAvatarPhoto sets the kid's signed photo link (the 320 px thumbnail, plenty for an avatar)
// when they have a photo avatar. Callers have checked the principal may read the kid profile.
func (h *handlers) withAvatarPhoto(ctx context.Context, k kids.Kid) (kids.Kid, error) {
	link, exp, err := h.avatarPhotoLink(ctx, k.ID)
	if err != nil {
		return k, err
	}
	k.AvatarPhotoURL, k.AvatarPhotoExpiresAt = link, exp
	return k, nil
}

// avatarPhotoLink signs a link to kid's photo avatar thumbnail; the link is empty when there is none.
func (h *handlers) avatarPhotoLink(ctx context.Context, kid int64) (string, *time.Time, error) {
	m, err := h.media.KidAvatar(ctx, kid)
	if err != nil || m == nil {
		return "", nil, err
	}
	urls, err := h.signedURLs(ctx, *m)
	if err != nil {
		return "", nil, err
	}
	return urls.ThumbURL, &urls.Expires, nil
}

// manageKid resolves a kid the caller may change the avatar of.
func (h *handlers) manageKid(ctx context.Context, ref string) (kids.Kid, error) {
	k, err := h.kids.Get(ctx, ref)
	if err != nil {
		return kids.Kid{}, err
	}
	p, _ := auth.PrincipalFrom(ctx)
	if !p.CanAccessKid(k.ID) {
		return kids.Kid{}, apierr.New(http.StatusForbidden, apierr.CodeForbidden, "you can't change this kid's profile")
	}
	return k, nil
}

type SetKidAvatarPhotoInput struct {
	Kid     string `path:"kid" doc:"Kid id or slug"`
	RawBody huma.MultipartFormFiles[MediaUploadForm]
}

func (h *handlers) setKidAvatarPhoto(ctx context.Context, in *SetKidAvatarPhotoInput) (*KidOutput, error) {
	f := in.RawBody.Data().File
	defer func() { _ = f.Close() }()
	k, err := h.manageKid(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	limit := h.cfg.Media.MaxPhotoBytes()
	if f.Size > limit {
		return nil, media.TooLarge(limit)
	}
	p, _ := auth.PrincipalFrom(ctx)
	if _, err := h.media.SetKidAvatar(ctx, p, k.ID, f, limit); err != nil {
		return nil, err
	}
	return h.kidOutput(ctx, k)
}

func (h *handlers) removeKidAvatarPhoto(ctx context.Context, in *KidRefInput) (*KidOutput, error) {
	k, err := h.manageKid(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	if err := h.media.ReleaseKidAvatar(ctx, k.ID); err != nil {
		return nil, err
	}
	return h.kidOutput(ctx, k)
}
