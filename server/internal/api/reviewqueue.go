package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/paging"
	"github.com/voidrot/chorecast/server/internal/reviewqueue"
)

func registerReviewQueue(hapi huma.API, h *handlers) {
	review := auth.Require(auth.ScopeChoresReview)
	huma.Register(hapi, huma.Operation{
		OperationID: "listReviewQueue", Method: http.MethodGet, Path: "/review-queue",
		Summary: "Chores waiting for review",
		Description: "`submitted` instances, oldest submission first (a flat list: the client groups it by `kidName` / " +
			"`instance.completedBy`). Each item has the chore's checklist steps with those the latest submission ticked, " +
			"its note and its photos with signed thumbnail links (valid for 10 minutes; `expired` once retention deleted " +
			"a photo), and the instance's rejection count and latest rejection. Filter by `kidId` (a shared chore matches " +
			"its members), `choreId`, and `media`: `with` (the latest submission has photos) or `without`. Parents only. " +
			"Approve one with `POST /instances/{id}/approval`, several with `POST /review-queue/approvals`, and reject " +
			"with `POST /instances/{id}/rejection`.",
		Tags: []string{"review"}, Security: review,
		Errors: []int{http.StatusForbidden, http.StatusBadRequest, http.StatusUnprocessableEntity},
	}, h.listReviewQueue)
	huma.Register(hapi, huma.Operation{
		OperationID: "approveReviewQueue", Method: http.MethodPost, Path: "/review-queue/approvals",
		Summary: "Approve several submitted chores",
		Description: "Approves up to 100 instances, each in its own transaction, so one failing doesn't undo the others. " +
			"The response has a result per distinct id, in request order: `approved`, or an error `code` such as " +
			"`instance_not_submitted` or `instance_not_found`. Rejecting isn't offered in bulk, because it needs " +
			"feedback per chore. Parents only.",
		Tags: []string{"review"}, Security: review, Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity},
	}, h.approveReviewQueue)
}

type ListReviewQueueInput struct {
	paging.Params
	KidID   int64  `query:"kidId" minimum:"0" doc:"Only this kid's chores"`
	ChoreID int64  `query:"choreId" minimum:"0" doc:"Only this chore's instances"`
	Media   string `query:"media" doc:"with = the latest submission has photos; without = it has none; omit for both"`
}

type ReviewQueueOutput struct {
	Body paging.Page[reviewqueue.ReviewQueueItem]
}

func (h *handlers) listReviewQueue(ctx context.Context, in *ListReviewQueueInput) (*ReviewQueueOutput, error) {
	f := reviewqueue.Filter{KidID: in.KidID, ChoreID: in.ChoreID}
	switch in.Media {
	case "":
		f.Media = reviewqueue.MediaAny
	case "with":
		f.Media = reviewqueue.MediaWith
	case "without":
		f.Media = reviewqueue.MediaWithout
	default:
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed",
			&huma.ErrorDetail{Location: "query.media", Message: "use with or without", Value: in.Media})
	}
	page, err := reviewqueue.List(ctx, h.db.Queries, h.clock, f, in.Params, h.signedURLs)
	if err != nil {
		return nil, err
	}
	return &ReviewQueueOutput{Body: page}, nil
}

type ApproveReviewQueueInput struct {
	Body struct {
		InstanceIDs []int64 `json:"instanceIds" minItems:"1" maxItems:"100" doc:"Instances to approve; repeats are approved once"`
	}
}

// ReviewQueueApprovalResult is one instance's outcome in a bulk approval.
type ReviewQueueApprovalResult struct {
	InstanceID int64  `json:"instanceId"`
	Approved   bool   `json:"approved"`
	Code       string `json:"code,omitempty" doc:"Why it wasn't approved, e.g. instance_not_submitted or instance_not_found"`
	Message    string `json:"message,omitempty"`
}

type ApproveReviewQueueOutput struct {
	Body struct {
		Results []ReviewQueueApprovalResult `json:"results" nullable:"false"`
	}
}

func (h *handlers) approveReviewQueue(ctx context.Context, in *ApproveReviewQueueInput) (*ApproveReviewQueueOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	out := &ApproveReviewQueueOutput{}
	out.Body.Results = []ReviewQueueApprovalResult{}
	var seen []int64
	for _, id := range in.Body.InstanceIDs {
		if slices.Contains(seen, id) {
			continue
		}
		seen = append(seen, id)
		_, err := h.instances.Approve(ctx, id, p.UserID)
		var prob *apierr.Problem
		switch {
		case err == nil:
			out.Body.Results = append(out.Body.Results, ReviewQueueApprovalResult{InstanceID: id, Approved: true})
		case errors.As(err, &prob) && prob.Status < http.StatusInternalServerError:
			out.Body.Results = append(out.Body.Results, ReviewQueueApprovalResult{InstanceID: id, Code: prob.Code, Message: prob.Detail})
		default:
			// Report it for this id only: the others' approvals are committed already.
			slog.ErrorContext(ctx, "bulk approve", "instance", id, "err", err)
			out.Body.Results = append(out.Body.Results, ReviewQueueApprovalResult{InstanceID: id, Code: "internal_error", Message: "could not approve this chore"})
		}
	}
	return out, nil
}
