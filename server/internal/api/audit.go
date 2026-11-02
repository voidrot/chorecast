package api

import (
	"context"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/paging"
)

func registerAudit(hapi huma.API, h *handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "listAuditLog", Method: http.MethodGet, Path: "/audit",
		Summary: "The household's audit log, newest first (owner only)",
		Description: "Who changed settings, approved, rejected, un-approved or excused chores, adjusted or reversed points, " +
			"invited, promoted, demoted, removed or reset parents, paired or revoked devices, and archived or unarchived kids. " +
			"`actorKind` is `parent` (with `actorId` and `actorName`), `system` (e.g. an auto-approval) or `cli` " +
			"(`chorecast admin` on the server host). `summary` is the target's name when it happened and `details` holds " +
			"the action's specifics; the server stores no display text. Filters combine; `from` and `to` are household-local " +
			"days, both inclusive. Entries are purged after 365 days.",
		Tags: []string{"audit"}, Security: auth.Require(auth.ScopeServerManage),
		Errors: []int{http.StatusForbidden, http.StatusBadRequest, http.StatusUnprocessableEntity},
	}, h.listAudit)
}

type ListAuditInput struct {
	paging.Params
	ActorKind string `query:"actorKind" doc:"Only this kind of actor: parent, system or cli"`
	ActorID   int64  `query:"actorId" minimum:"0" doc:"Only this parent's actions"`
	Action    string `query:"action" doc:"Only this action, e.g. kid_archived"`
	From      string `query:"from" doc:"First day (YYYY-MM-DD), household time"`
	To        string `query:"to" doc:"Last day (YYYY-MM-DD), household time, inclusive"`
}

type AuditLogOutput struct {
	Body paging.Page[audit.AuditEntry]
}

func (h *handlers) listAudit(ctx context.Context, in *ListAuditInput) (*AuditLogOutput, error) {
	f := audit.Filter{ActorKind: in.ActorKind, ActorID: in.ActorID, Action: in.Action}
	var details []error
	bad := func(name, msg, v string) {
		details = append(details, &huma.ErrorDetail{Location: "query." + name, Message: msg, Value: v})
	}
	if in.ActorKind != "" && !slices.Contains(audit.ActorKinds, in.ActorKind) {
		bad("actorKind", "use parent, system or cli", in.ActorKind)
	}
	if in.Action != "" && !slices.Contains(audit.Actions, in.Action) {
		bad("action", "unknown action", in.Action)
	}
	var from, to caltime.Date
	parse := func(name, v string, into *caltime.Date) {
		if v == "" {
			return
		}
		d, err := caltime.ParseDate(v)
		if err != nil {
			bad(name, "use YYYY-MM-DD", v)
			return
		}
		*into = d
	}
	parse("from", in.From, &from)
	parse("to", in.To, &to)
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		bad("to", "to is before from", in.To)
	}
	if len(details) > 0 {
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}
	if !from.IsZero() || !to.IsZero() {
		cal, err := household.Calendar(ctx, h.db.Queries)
		if err != nil {
			return nil, err
		}
		if !from.IsZero() {
			f.Since = cal.StartOfDay(from)
		}
		if !to.IsZero() {
			f.Until = cal.StartOfDay(to.AddDays(1))
		}
	}
	page, err := h.audit.List(ctx, f, in.Params)
	if err != nil {
		return nil, err
	}
	return &AuditLogOutput{Body: page}, nil
}

// caller is the signed-in parent acting, for the audit log. Every audited operation requires a
// parent scope, so the principal is a parent (on the web, in the app, or elevated on a device).
func caller(ctx context.Context) audit.Actor {
	p, _ := auth.PrincipalFrom(ctx) // guaranteed by the operation's security policy
	return audit.Parent(p.UserID)
}
