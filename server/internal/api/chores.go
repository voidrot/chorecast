package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/chores"
)

func registerChores(hapi huma.API, h *handlers) {
	read, manage := auth.Require(auth.ScopeChoresRead), auth.Require(auth.ScopeChoresManage)
	huma.Register(hapi, huma.Operation{
		OperationID: "listChores", Method: http.MethodGet, Path: "/chores",
		Summary: "Chore definitions",
		Description: "Parents and displays get every chore. A kid gets the chores assigned to them (everyone, an auto-pick from every kid, " +
			"or listed in the assignment, unless an exception excludes them) and those they have an instance of.",
		Tags: []string{"chores"}, Security: read, Errors: []int{http.StatusForbidden},
	}, h.listChores)
	huma.Register(hapi, huma.Operation{
		OperationID: "getChore", Method: http.MethodGet, Path: "/chores/{id}",
		Summary:     "A chore definition",
		Description: "A kid gets 404 for a chore they can't see (see `listChores`).",
		Tags:        []string{"chores"}, Security: read, Errors: []int{http.StatusForbidden, http.StatusNotFound},
	}, h.getChore)
	huma.Register(hapi, huma.Operation{
		OperationID: "createChore", Method: http.MethodPost, Path: "/chores",
		Summary: "Add a chore", Tags: []string{"chores"}, Security: manage, DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusUnprocessableEntity},
	}, h.createChore)
	huma.Register(hapi, huma.Operation{
		OperationID: "updateChore", Method: http.MethodPut, Path: "/chores/{id}",
		Summary:     "Edit a chore",
		Description: "Replaces the definition; optional fields left out are cleared. Upcoming untouched instances are regenerated.",
		Tags:        []string{"chores"}, Security: manage, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, h.updateChore)
	huma.Register(hapi, huma.Operation{
		OperationID: "deleteChore", Method: http.MethodDelete, Path: "/chores/{id}",
		Summary:     "Delete a chore",
		Description: "Deletes the chore and its upcoming instances. A chore with history (an instance that's done, missed, reassigned or already due) can't be deleted: disable it instead (409 `chore_has_history`).",
		Tags:        []string{"chores"}, Security: manage, DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, h.deleteChore)

	huma.Register(hapi, huma.Operation{
		OperationID: "previewSchedule", Method: http.MethodPost, Path: "/chores/preview-schedule",
		Summary:     "Preview a schedule rule",
		Description: "Validates a rule and returns its next occurrences from today in the household timezone, without saving anything. POST because the rule is a JSON body.",
		Tags:        []string{"chores"}, Security: manage, Errors: []int{http.StatusUnprocessableEntity},
	}, h.previewSchedule)
	huma.Register(hapi, huma.Operation{
		OperationID: "getStarterPack", Method: http.MethodGet, Path: "/chores/starter-pack",
		Summary: "The starter pack",
		Description: "Suggested chores, locations and equipment for a new household, each marked when one with the same name " +
			"(any case) exists.",
		Tags: []string{"chores"}, Security: manage, Errors: []int{http.StatusForbidden},
	}, h.getStarterPack)
	huma.Register(hapi, huma.Operation{
		OperationID: "applyStarterPack", Method: http.MethodPost, Path: "/chores/starter-pack/apply",
		Summary: "Add chores from the starter pack",
		Description: "Creates the selected chores and the locations and equipment they use, in one transaction. Chores whose name " +
			"exists are skipped; locations and equipment with the same name are reused. Repeating the call creates nothing new.",
		Tags: []string{"chores"}, Security: manage, Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity},
	}, h.applyStarterPack)

	registerCatalog(hapi, "Location", "/locations", "locations", func() *chores.Catalog { return h.chores.Locations() })
	registerCatalog(hapi, "Equipment", "/equipment", "equipment", func() *chores.Catalog { return h.chores.Equipment() })
}

// errChoresForbidden refuses chore reads to principal kinds without a visibility rule (fail closed).
var errChoresForbidden = apierr.New(http.StatusForbidden, apierr.CodeForbidden, "you can't read chores")

type ChoresOutput struct {
	Body struct {
		Items []chores.Chore `json:"items" nullable:"false"`
	}
}

func (h *handlers) listChores(ctx context.Context, _ *struct{}) (*ChoresOutput, error) {
	var items []chores.Chore
	var err error
	switch p, _ := auth.PrincipalFrom(ctx); p.Kind {
	case auth.KindParent, auth.KindDisplay:
		items, err = h.chores.List(ctx)
	case auth.KindKid:
		items, err = h.chores.ListForKid(ctx, p.UserID)
	default:
		return nil, errChoresForbidden
	}
	if err != nil {
		return nil, err
	}
	out := &ChoresOutput{}
	out.Body.Items = items
	return out, nil
}

type ChoreIDInput struct {
	ID int64 `path:"id"`
}

type ChoreOutput struct {
	Body chores.Chore
}

func (h *handlers) getChore(ctx context.Context, in *ChoreIDInput) (*ChoreOutput, error) {
	var c chores.Chore
	var err error
	switch p, _ := auth.PrincipalFrom(ctx); p.Kind {
	case auth.KindParent, auth.KindDisplay:
		c, err = h.chores.Get(ctx, in.ID)
	case auth.KindKid:
		c, err = h.chores.GetForKid(ctx, in.ID, p.UserID)
	default:
		return nil, errChoresForbidden
	}
	if err != nil {
		return nil, err
	}
	return &ChoreOutput{Body: c}, nil
}

type CreateChoreInput struct {
	Body chores.ChoreInput
}

func (h *handlers) createChore(ctx context.Context, in *CreateChoreInput) (*ChoreOutput, error) {
	c, err := h.chores.Create(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &ChoreOutput{Body: c}, nil
}

type UpdateChoreInput struct {
	ID   int64 `path:"id"`
	Body chores.ChoreInput
}

func (h *handlers) updateChore(ctx context.Context, in *UpdateChoreInput) (*ChoreOutput, error) {
	c, err := h.chores.Update(ctx, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ChoreOutput{Body: c}, nil
}

func (h *handlers) deleteChore(ctx context.Context, in *ChoreIDInput) (*struct{}, error) {
	return nil, h.chores.Delete(ctx, in.ID)
}

type PreviewScheduleInput struct {
	Body chores.SchedulePreviewInput
}

type PreviewScheduleOutput struct {
	Body chores.SchedulePreview
}

func (h *handlers) previewSchedule(ctx context.Context, in *PreviewScheduleInput) (*PreviewScheduleOutput, error) {
	p, err := h.chores.PreviewSchedule(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &PreviewScheduleOutput{Body: p}, nil
}

type StarterPackOutput struct {
	Body chores.StarterPack
}

func (h *handlers) getStarterPack(ctx context.Context, _ *struct{}) (*StarterPackOutput, error) {
	p, err := h.chores.StarterPack(ctx)
	if err != nil {
		return nil, err
	}
	return &StarterPackOutput{Body: p}, nil
}

type ApplyStarterInput struct {
	Body chores.StarterApplyInput
}

type ApplyStarterOutput struct {
	Body chores.StarterApplyResult
}

func (h *handlers) applyStarterPack(ctx context.Context, in *ApplyStarterInput) (*ApplyStarterOutput, error) {
	r, err := h.chores.ApplyStarter(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &ApplyStarterOutput{Body: r}, nil
}

type CatalogOutput struct {
	Body struct {
		Items []chores.CatalogItem `json:"items" nullable:"false"`
	}
}

type CatalogItemOutput struct {
	Body chores.CatalogItem
}

type CatalogCreateInput struct {
	Body chores.CatalogInput
}

type CatalogUpdateInput struct {
	ID   int64 `path:"id"`
	Body chores.CatalogInput
}

type CatalogIDInput struct {
	ID int64 `path:"id"`
}

// registerCatalog registers list/create/update/delete for one catalog (locations or equipment).
func registerCatalog(hapi huma.API, name, path, plural string, catalog func() *chores.Catalog) {
	read, manage := auth.Require(auth.ScopeChoresRead), auth.Require(auth.ScopeChoresManage)
	huma.Register(hapi, huma.Operation{
		OperationID: "list" + name, Method: http.MethodGet, Path: path,
		Summary: "The " + plural + " catalog", Tags: []string{"chores"}, Security: read,
	}, func(ctx context.Context, _ *struct{}) (*CatalogOutput, error) {
		items, err := catalog().List(ctx)
		if err != nil {
			return nil, err
		}
		out := &CatalogOutput{}
		out.Body.Items = items
		return out, nil
	})
	huma.Register(hapi, huma.Operation{
		OperationID: "create" + name, Method: http.MethodPost, Path: path,
		Summary: "Add to the " + plural + " catalog", Tags: []string{"chores"}, Security: manage,
		DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *CatalogCreateInput) (*CatalogItemOutput, error) {
		item, err := catalog().Create(ctx, in.Body)
		if err != nil {
			return nil, err
		}
		return &CatalogItemOutput{Body: item}, nil
	})
	huma.Register(hapi, huma.Operation{
		OperationID: "update" + name, Method: http.MethodPut, Path: path + "/{id}",
		Summary: "Edit a " + plural + " catalog entry", Tags: []string{"chores"}, Security: manage,
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *CatalogUpdateInput) (*CatalogItemOutput, error) {
		item, err := catalog().Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return &CatalogItemOutput{Body: item}, nil
	})
	huma.Register(hapi, huma.Operation{
		OperationID: "delete" + name, Method: http.MethodDelete, Path: path + "/{id}",
		Summary: "Delete a " + plural + " catalog entry (chores using it lose the link)", Tags: []string{"chores"},
		Security: manage, DefaultStatus: http.StatusNoContent, Errors: []int{http.StatusNotFound},
	}, func(ctx context.Context, in *CatalogIDInput) (*struct{}, error) {
		return nil, catalog().Delete(ctx, in.ID)
	})
}
