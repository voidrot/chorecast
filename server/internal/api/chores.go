package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/chores"
)

func registerChores(hapi huma.API, h *handlers) {
	read, manage := auth.Require(auth.ScopeChoresRead), auth.Require(auth.ScopeChoresManage)
	huma.Register(hapi, huma.Operation{
		OperationID: "listChores", Method: http.MethodGet, Path: "/chores",
		Summary: "Chore definitions", Tags: []string{"chores"}, Security: read,
	}, h.listChores)
	huma.Register(hapi, huma.Operation{
		OperationID: "getChore", Method: http.MethodGet, Path: "/chores/{id}",
		Summary: "A chore definition", Tags: []string{"chores"}, Security: read, Errors: []int{http.StatusNotFound},
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

	registerCatalog(hapi, "Location", "/locations", "locations", func() *chores.Catalog { return h.chores.Locations() })
	registerCatalog(hapi, "Equipment", "/equipment", "equipment", func() *chores.Catalog { return h.chores.Equipment() })
}

type ChoresOutput struct {
	Body struct {
		Items []chores.Chore `json:"items" nullable:"false"`
	}
}

func (h *handlers) listChores(ctx context.Context, _ *struct{}) (*ChoresOutput, error) {
	items, err := h.chores.List(ctx)
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
	c, err := h.chores.Get(ctx, in.ID)
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
