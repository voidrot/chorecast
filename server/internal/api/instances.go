package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/horizon"
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
