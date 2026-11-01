package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/ledger"
	"github.com/voidrot/chorecast/server/internal/paging"
)

func registerPoints(hapi huma.API, h *handlers) {
	read := auth.Require(auth.ScopePointsRead)
	manage := auth.Require(auth.ScopePointsManage)
	huma.Register(hapi, huma.Operation{
		OperationID: "getKidPointsBalance",
		Method:      http.MethodGet,
		Path:        "/kids/{kid}/points",
		Summary:     "A kid's points balance",
		Description: "Kids can read only their own.",
		Tags:        []string{"points"},
		Security:    read,
		Errors:      []int{http.StatusForbidden, http.StatusNotFound},
	}, h.getKidPointsBalance)
	huma.Register(hapi, huma.Operation{
		OperationID: "listKidPointsEntries",
		Method:      http.MethodGet,
		Path:        "/kids/{kid}/points/entries",
		Summary:     "A kid's points ledger, newest first",
		Description: "Each entry carries the balance after it. Kids can read only their own.",
		Tags:        []string{"points"},
		Security:    read,
		Errors:      []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound},
	}, h.listKidPointsEntries)
	huma.Register(hapi, huma.Operation{
		OperationID:   "createKidPointsAdjustment",
		Method:        http.MethodPost,
		Path:          "/kids/{kid}/points/adjustments",
		Summary:       "Add or subtract points by hand",
		Description:   "A note is required. With negative balances off, a deduction is clamped to the balance and the entry's `requestedAmount` records what was asked.",
		Tags:          []string{"points"},
		Security:      manage,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, h.createKidPointsAdjustment)
	huma.Register(hapi, huma.Operation{
		OperationID:   "reverseKidPointsEntry",
		Method:        http.MethodPost,
		Path:          "/kids/{kid}/points/entries/{entry}/reversal",
		Summary:       "Reverse a points entry",
		Description:   "Appends a reversal with the negated amount; nothing is edited or deleted. A note is required. An entry can be reversed once (409 `already_reversed`); a reversal can itself be reversed.",
		Tags:          []string{"points"},
		Security:      manage,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, h.reverseKidPointsEntry)
}

// pointsKid resolves the path's kid and checks the caller may see their points. Archived kids
// are visible to parents only.
func (h *handlers) pointsKid(ctx context.Context, ref string) (int64, *auth.Principal, error) {
	k, err := kids.Resolve(ctx, h.db.Queries, ref)
	if err != nil {
		return 0, nil, err
	}
	p, _ := auth.PrincipalFrom(ctx)
	if k.ArchivedAt.Valid && !p.Has(auth.ScopeHouseholdManage) {
		return 0, nil, apierr.New(http.StatusNotFound, apierr.CodeKidNotFound, "no such kid")
	}
	if !p.CanAccessKid(k.ID) {
		return 0, nil, apierr.New(http.StatusForbidden, apierr.CodeForbidden, "you can only see your own points")
	}
	return k.ID, p, nil
}

type PointsBalanceOutput struct {
	Body ledger.PointsBalance
}

func (h *handlers) getKidPointsBalance(ctx context.Context, in *KidRefInput) (*PointsBalanceOutput, error) {
	kidID, _, err := h.pointsKid(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	b, err := h.ledger.Balance(ctx, kidID)
	if err != nil {
		return nil, err
	}
	return &PointsBalanceOutput{Body: b}, nil
}

type ListKidPointsEntriesInput struct {
	Kid string `path:"kid" doc:"Kid id or slug"`
	paging.Params
}

type PointsEntriesOutput struct {
	Body paging.Page[ledger.PointsEntry]
}

func (h *handlers) listKidPointsEntries(ctx context.Context, in *ListKidPointsEntriesInput) (*PointsEntriesOutput, error) {
	kidID, _, err := h.pointsKid(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	page, err := h.ledger.List(ctx, kidID, in.Params)
	if err != nil {
		return nil, err
	}
	return &PointsEntriesOutput{Body: page}, nil
}

// PointsAdjustmentInput is a parent's manual points change.
type PointsAdjustmentInput struct {
	Amount int64  `json:"amount" minimum:"-1000000" maximum:"1000000" doc:"Points to add (positive) or subtract (negative); not zero"`
	Note   string `json:"note" minLength:"1" maxLength:"500" doc:"Why; shown to the kid"`
}

type CreateKidPointsAdjustmentInput struct {
	Kid  string `path:"kid" doc:"Kid id or slug"`
	Body PointsAdjustmentInput
}

type PointsEntryOutput struct {
	Body ledger.PointsEntry
}

func (h *handlers) createKidPointsAdjustment(ctx context.Context, in *CreateKidPointsAdjustmentInput) (*PointsEntryOutput, error) {
	kidID, p, err := h.pointsKid(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	e, err := h.ledger.Adjust(ctx, kidID, in.Body.Amount, in.Body.Note, p.UserID)
	if err != nil {
		return nil, err
	}
	return &PointsEntryOutput{Body: e}, nil
}

// PointsReversalInput says why an entry is reversed.
type PointsReversalInput struct {
	Note string `json:"note" minLength:"1" maxLength:"500" doc:"Why; shown to the kid"`
}

type ReverseKidPointsEntryInput struct {
	Kid   string `path:"kid" doc:"Kid id or slug"`
	Entry int64  `path:"entry" doc:"The entry to reverse"`
	Body  PointsReversalInput
}

func (h *handlers) reverseKidPointsEntry(ctx context.Context, in *ReverseKidPointsEntryInput) (*PointsEntryOutput, error) {
	kidID, p, err := h.pointsKid(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	e, err := h.ledger.ReverseKidEntry(ctx, kidID, in.Entry, in.Body.Note, p.UserID)
	if err != nil {
		return nil, err
	}
	return &PointsEntryOutput{Body: e}, nil
}
