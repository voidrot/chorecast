package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/inbox"
	"github.com/voidrot/chorecast/server/internal/paging"
)

func registerInbox(hapi huma.API, h *handlers) {
	use := auth.Require(auth.ScopeInboxUse)
	huma.Register(hapi, huma.Operation{
		OperationID: "listInbox", Method: http.MethodGet, Path: "/inbox",
		Summary: "The caller's notifications, newest delivered first",
		Description: "Parents see their own (new submissions to review); a kid, on their own device or picked on a shared " +
			"tablet, sees theirs (approved, rejected with the reason, missed, excused, streak milestones). Each item has a " +
			"`kind`, what it refers to (`refKind` and `refId`) and `data` to render the text from; the server stores no " +
			"display text. Order is by delivery (id), so a late-delivered event can sit above an older-looking one; `createdAt` is when the event happened and `data.choreName` is the chore's name at delivery. `unreadCount` counts all unread, not just this page. Notifications are purged after 90 days.",
		Tags: []string{"inbox"}, Security: use,
		Errors: []int{http.StatusForbidden, http.StatusBadRequest},
	}, h.listInbox)
	huma.Register(hapi, huma.Operation{
		OperationID: "markInboxRead", Method: http.MethodPost, Path: "/inbox/{id}/read",
		Summary:       "Mark one notification read",
		Description:   "Marking a read notification again changes nothing. Someone else's notification is 404.",
		Tags:          []string{"inbox"},
		Security:      use,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden, http.StatusNotFound},
	}, h.markInboxRead)
	huma.Register(hapi, huma.Operation{
		OperationID:   "markAllInboxRead",
		Method:        http.MethodPost,
		Path:          "/inbox/read-all",
		Summary:       "Mark every notification read",
		Tags:          []string{"inbox"},
		Security:      use,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden},
	}, h.markAllInboxRead)
}

type ListInboxInput struct{ paging.Params }

type InboxOutput struct{ Body inbox.InboxPage }

// inboxRecipient is the caller's user id: the inbox belongs to a parent or a kid, never to a
// shared tablet or display itself (inbox:use excludes them, and a caller without a user is
// refused here as well). A tablet with a kid profile picked is that kid.
func inboxRecipient(ctx context.Context) (int64, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.UserID == 0 {
		return 0, apierr.New(http.StatusForbidden, apierr.CodeInsufficientScope, "this credential has no inbox")
	}
	return p.UserID, nil
}

func (h *handlers) listInbox(ctx context.Context, in *ListInboxInput) (*InboxOutput, error) {
	me, err := inboxRecipient(ctx)
	if err != nil {
		return nil, err
	}
	page, err := h.inbox.List(ctx, me, in.Params)
	if err != nil {
		return nil, err
	}
	return &InboxOutput{Body: page}, nil
}

type MarkInboxReadInput struct {
	ID int64 `path:"id" minimum:"1"`
}

func (h *handlers) markInboxRead(ctx context.Context, in *MarkInboxReadInput) (*struct{}, error) {
	me, err := inboxRecipient(ctx)
	if err != nil {
		return nil, err
	}
	return nil, h.inbox.MarkRead(ctx, me, in.ID)
}

func (h *handlers) markAllInboxRead(ctx context.Context, _ *struct{}) (*struct{}, error) {
	me, err := inboxRecipient(ctx)
	if err != nil {
		return nil, err
	}
	return nil, h.inbox.MarkAllRead(ctx, me)
}
