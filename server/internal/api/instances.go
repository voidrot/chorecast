package api

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/instances"
)

// maxInstanceRangeDays bounds one list request.
const maxInstanceRangeDays = 62

func registerInstances(hapi huma.API, h *handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "listChoreInstances", Method: http.MethodGet, Path: "/instances",
		Summary: "Chore instances by date",
		Description: "Instances on [from, to] (default today to today+7, at most 62 days). Kids see only their own " +
			"(including shared ones they're a member of); parents see every kid's, or one kid's with `kidId`. " +
			"Days after today are `planned`: a forecast that may still change.",
		Tags: []string{"instances"}, Security: auth.Require(auth.ScopeChoresRead),
		Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity},
	}, h.listInstances)

	// Approve, reject, unapprove, excuse and reassign are parent-only: only parents hold
	// chores:review and chores:manage, and parents may act for every kid.
	review := auth.Require(auth.ScopeChoresReview)
	transition := func(id, path, summary, desc string, security []map[string][]string, errs ...int) huma.Operation {
		return huma.Operation{
			OperationID: id, Method: http.MethodPost, Path: "/instances/{instance}/" + path, Summary: summary,
			Description: desc + " Returns the updated instance.", Tags: []string{"instances"}, Security: security,
			Errors: append([]int{http.StatusNotFound, http.StatusConflict}, errs...),
		}
	}
	huma.Register(hapi, transition("completeChoreInstance", "completion", "Complete a chore",
		"Marks an open instance done: `submitted` for a parent to approve, or `approved` with points credited "+
			"when the chore doesn't require approval. Kids complete their own instances (or shared ones they're a "+
			"member of); a parent completes for a kid and names the kid for a shared instance. An instance dated "+
			"after today can't be completed yet (409 `instance_planned`), nor one whose day (or week) has closed "+
			"(409 `instance_closed`): day close counts it as missed.",
		auth.Require(auth.ScopeChoresComplete), http.StatusForbidden, http.StatusUnprocessableEntity), h.completeInstance)
	huma.Register(hapi, transition("approveChoreInstance", "approval", "Approve a submitted chore",
		"Credits the points: to the assignee, or for a shared chore to the completer or split evenly across "+
			"the group. Re-approving after a reversal restores the earlier credit with its amount: the current "+
			"assignee's or completer's (or posts theirs if they have none), or an even split's first split, "+
			"which isn't re-split if the points or the completer changed.", review), h.approveInstance)
	huma.Register(hapi, transition("rejectChoreInstance", "rejection", "Reject a submitted chore",
		"Sends it back to `open` with a reason (a quick pick, text, or both) so the kid can redo it. "+
			"The instance keeps a rejection count and its latest rejection.", review, http.StatusUnprocessableEntity), h.rejectInstance)
	huma.Register(hapi, transition("reverseChoreInstanceApproval", "approval/reversal", "Reverse an approval",
		"Sends an approved instance back to `open` and reverses its credit.", review, http.StatusUnprocessableEntity), h.unapproveInstance)
	huma.Register(hapi, transition("excuseChoreInstance", "excusal", "Excuse a missed chore",
		"Marks a missed instance `excused` and reverses its penalty (every member's, for a shared chore).",
		review, http.StatusUnprocessableEntity), h.excuseInstance)
	huma.Register(hapi, transition("reassignChoreInstance", "reassignment", "Give an open chore to another kid",
		"Marks it manually reassigned, so regenerating the horizon keeps it (as the original kid's occurrence). "+
			"Without a slot, it becomes due at the new kid's day (or week) close. Shared instances can't be "+
			"reassigned (409 `instance_shared`), nor moved to a kid who already has the same occurrence "+
			"(409 `instance_taken`), or to a kid whose day (or week) has closed (409 `instance_closed`).", auth.Require(auth.ScopeChoresManage), http.StatusUnprocessableEntity), h.reassignInstance)
}

type ListChoreInstancesInput struct {
	KidID int64  `query:"kidId" minimum:"0" doc:"Only this kid's instances; kids default to themselves"`
	From  string `query:"from" doc:"First day (YYYY-MM-DD); default today"`
	To    string `query:"to" doc:"Last day (YYYY-MM-DD); default today+7"`
}

type ChoreInstancesOutput struct {
	Body struct {
		Items []horizon.ChoreInstance `json:"items" nullable:"false"`
	}
}

func (h *handlers) listInstances(ctx context.Context, in *ListChoreInstancesInput) (*ChoreInstancesOutput, error) {
	p, _ := auth.PrincipalFrom(ctx) // guaranteed by the operation's security policy
	kid := in.KidID
	if kid == 0 && p.Kind == auth.KindKid {
		kid = p.UserID
	}
	everyKid := p.Kind == auth.KindParent || p.Kind == auth.KindDisplay
	if (kid == 0 && !everyKid) || (kid != 0 && !p.CanAccessKid(kid)) {
		return nil, apierr.New(http.StatusForbidden, apierr.CodeForbidden, "you can't see this kid's chores")
	}
	from, to, err := h.horizon.Range(ctx)
	if err != nil {
		return nil, err
	}
	var details []error
	parse := func(name, v string, into *caltime.Date) {
		if v == "" {
			return
		}
		d, err := caltime.ParseDate(v)
		if err != nil {
			details = append(details, &huma.ErrorDetail{Location: "query." + name, Message: "use YYYY-MM-DD", Value: v})
			return
		}
		*into = d
	}
	parse("from", in.From, &from)
	if in.From != "" && in.To == "" {
		to = from.AddDays(horizon.Days)
	}
	parse("to", in.To, &to)
	if len(details) == 0 {
		switch n := from.DaysUntil(to); {
		case n < 0:
			details = append(details, &huma.ErrorDetail{Location: "query.to", Message: "to is before from", Value: in.To})
		case n >= maxInstanceRangeDays:
			details = append(details, &huma.ErrorDetail{Location: "query.to", Message: "at most 62 days at a time", Value: in.To})
		}
	}
	if len(details) > 0 {
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}
	items, err := h.horizon.List(ctx, horizon.ListFilter{KidID: kid, From: from, To: to})
	if err != nil {
		return nil, err
	}
	out := &ChoreInstancesOutput{}
	out.Body.Items = items
	return out, nil
}

type ChoreInstanceOutput struct {
	Body horizon.ChoreInstance
}

type ChoreInstanceRefInput struct {
	Instance int64 `path:"instance" doc:"Instance id"`
}

type CompleteChoreInstanceInput struct {
	Instance int64 `path:"instance" doc:"Instance id"`
	Body     *struct {
		KidID int64 `json:"kidId,omitempty" doc:"Parents: the kid who did it (required for a shared instance). Kids complete as themselves."`
	}
}

func (h *handlers) completeInstance(ctx context.Context, in *CompleteChoreInstanceInput) (*ChoreInstanceOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	kid := int64(0)
	if in.Body != nil {
		kid = in.Body.KidID
	}
	inst, err := h.instances.Get(ctx, in.Instance)
	if err != nil {
		return nil, err
	}
	if p.Kind == auth.KindKid {
		mine := inst.KidID == p.UserID || slices.Contains(inst.GroupKidIDs, p.UserID)
		if !mine || (kid != 0 && kid != p.UserID) {
			return nil, apierr.New(http.StatusForbidden, apierr.CodeForbidden, "you can only complete your own chores")
		}
		kid = p.UserID
	}
	// The kid it's completed for: the one named, else the assignee. Complete gets that kid, so if
	// the instance was reassigned since, it refuses (422) instead of completing it for a kid who
	// wasn't checked. A shared instance needs a named member, which Complete checks too.
	target := cmp.Or(kid, inst.KidID)
	if target != 0 && !p.CanAccessKid(target) {
		return nil, apierr.New(http.StatusForbidden, apierr.CodeForbidden, "you can't complete this kid's chores")
	}
	return instanceOutput(h.instances.Complete(ctx, in.Instance, instances.Completion{KidID: target, By: p.UserID}))
}

func (h *handlers) approveInstance(ctx context.Context, in *ChoreInstanceRefInput) (*ChoreInstanceOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return instanceOutput(h.instances.Approve(ctx, in.Instance, p.UserID))
}

type RejectChoreInstanceInput struct {
	Instance int64 `path:"instance" doc:"Instance id"`
	Body     struct {
		Reason string `json:"reason,omitempty" enum:"not_done,not_finished,needs_redo,evidence_unclear" doc:"A quick-pick reason"`
		Text   string `json:"text,omitempty" maxLength:"500" doc:"Feedback in the parent's words"`
	}
}

func (h *handlers) rejectInstance(ctx context.Context, in *RejectChoreInstanceInput) (*ChoreInstanceOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	r := instances.Reason{Code: in.Body.Reason, Text: in.Body.Text}
	return instanceOutput(h.instances.Reject(ctx, in.Instance, p.UserID, r))
}

// ChoreInstanceNoteInput is an optional note on a reversal or an excusal.
type ChoreInstanceNoteInput struct {
	Instance int64 `path:"instance" doc:"Instance id"`
	Body     *struct {
		Note string `json:"note,omitempty" maxLength:"500" doc:"Why, on the reversing points entries; a default when absent"`
	}
}

func (in *ChoreInstanceNoteInput) note() string {
	if in.Body == nil {
		return ""
	}
	return in.Body.Note
}

func (h *handlers) unapproveInstance(ctx context.Context, in *ChoreInstanceNoteInput) (*ChoreInstanceOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return instanceOutput(h.instances.Unapprove(ctx, in.Instance, p.UserID, in.note()))
}

func (h *handlers) excuseInstance(ctx context.Context, in *ChoreInstanceNoteInput) (*ChoreInstanceOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return instanceOutput(h.instances.Excuse(ctx, in.Instance, p.UserID, in.note()))
}

type ReassignChoreInstanceInput struct {
	Instance int64 `path:"instance" doc:"Instance id"`
	Body     struct {
		KidID int64 `json:"kidId" minimum:"1" doc:"The kid who gets it"`
	}
}

func (h *handlers) reassignInstance(ctx context.Context, in *ReassignChoreInstanceInput) (*ChoreInstanceOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return instanceOutput(h.instances.Reassign(ctx, in.Instance, in.Body.KidID, p.UserID))
}

func instanceOutput(i horizon.ChoreInstance, err error) (*ChoreInstanceOutput, error) {
	if err != nil {
		return nil, err
	}
	return &ChoreInstanceOutput{Body: i}, nil
}
