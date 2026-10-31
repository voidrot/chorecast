package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/kids"
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
	return &KidOutput{Body: k}, nil
}

type CreateKidInput struct {
	Body kids.KidInput
}

func (h *handlers) createKid(ctx context.Context, in *CreateKidInput) (*KidOutput, error) {
	k, err := h.kids.Create(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &KidOutput{Body: k}, nil
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
	return &KidOutput{Body: k}, nil
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
	k, err := h.kids.Archive(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	return &KidOutput{Body: k}, nil
}

func (h *handlers) unarchiveKid(ctx context.Context, in *KidRefInput) (*KidOutput, error) {
	k, err := h.kids.Unarchive(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	return &KidOutput{Body: k}, nil
}
