package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/parents"
)

func registerParents(hapi huma.API, h *handlers) {
	owner := auth.Require(auth.ScopeParentsManage)
	huma.Register(hapi, huma.Operation{
		OperationID: "listParents",
		Method:      http.MethodGet,
		Path:        "/parents",
		Summary:     "The household's parents",
		Tags:        []string{"parents"},
		Security:    auth.Require(auth.ScopeHouseholdManage),
	}, h.listParents)
	huma.Register(hapi, huma.Operation{
		OperationID: "setParentRole",
		Method:      http.MethodPut,
		Path:        "/parents/{id}/role",
		Summary:     "Make a parent an owner, or an owner a parent (owners only)",
		Description: "The household always keeps at least one owner (409 last_owner).",
		Tags:        []string{"parents"},
		Security:    owner,
		Errors:      []int{http.StatusNotFound, http.StatusConflict},
	}, h.setParentRole)
	huma.Register(hapi, huma.Operation{
		OperationID:   "removeParent",
		Method:        http.MethodDelete,
		Path:          "/parents/{id}",
		Summary:       "Remove a parent (owners only)",
		Description:   "Signs them out everywhere and deletes their credentials; their name stays on past entries.",
		Tags:          []string{"parents"},
		Security:      owner,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusNotFound, http.StatusConflict},
	}, h.removeParent)
	huma.Register(hapi, huma.Operation{
		OperationID: "resetParentPassword",
		Method:      http.MethodPost,
		Path:        "/parents/{id}/reset-password",
		Summary:     "Give a parent a temporary password (owners only)",
		Description: "Returns a new random password, shown once, and signs the parent out everywhere.",
		Tags:        []string{"parents"},
		Security:    owner,
		Errors:      []int{http.StatusNotFound},
	}, h.resetParentPassword)
	huma.Register(hapi, huma.Operation{
		OperationID:   "disableParentTotp",
		Method:        http.MethodPost,
		Path:          "/parents/{id}/disable-totp",
		Summary:       "Turn off a parent's two-factor sign-in (owners only)",
		Tags:          []string{"parents"},
		Security:      owner,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusNotFound},
	}, h.disableParentTOTP)

	huma.Register(hapi, huma.Operation{
		OperationID: "createInvite",
		Method:      http.MethodPost,
		Path:        "/invites",
		Summary:     "Invite a parent (owners only)",
		Description: "Returns a single-use link (valid 7 days) to share or show as a QR code. The token is shown only now.",
		Tags:        []string{"parents"},
		Security:    owner,
	}, h.createInvite)
	huma.Register(hapi, huma.Operation{
		OperationID: "listInvites",
		Method:      http.MethodGet,
		Path:        "/invites",
		Summary:     "Pending invites (owners only)",
		Tags:        []string{"parents"},
		Security:    owner,
	}, h.listInvites)
	huma.Register(hapi, huma.Operation{
		OperationID:   "revokeInvite",
		Method:        http.MethodDelete,
		Path:          "/invites/{id}",
		Summary:       "Revoke a pending invite (owners only)",
		Tags:          []string{"parents"},
		Security:      owner,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusNotFound},
	}, h.revokeInvite)
	huma.Register(hapi, huma.Operation{
		OperationID: "getInvite",
		Method:      http.MethodPost,
		Path:        "/auth/invite",
		Summary:     "Check an invite before accepting it",
		Description: "POST so the token stays out of URLs and logs.",
		Tags:        []string{"auth"},
		Metadata:    auth.Public(),
		Errors:      []int{http.StatusGone},
	}, h.getInvite)
	huma.Register(hapi, huma.Operation{
		OperationID: "acceptInvite",
		Method:      http.MethodPost,
		Path:        "/auth/invite/accept",
		Summary:     "Accept an invite with a username and password",
		Description: "Creates the parent and signs them in (session cookie). To join with single sign-on instead, " +
			"navigate to /auth/oidc/start?client=invite&invite=…&displayName=….",
		Tags:     []string{"auth"},
		Metadata: auth.Public(),
		Errors:   []int{http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity},
	}, h.acceptInvite)
}

type ParentIDInput struct {
	ID int64 `path:"id"`
}

type ParentsOutput struct {
	Body struct {
		Items []parents.Parent `json:"items" nullable:"false"`
	}
}

func (h *handlers) listParents(ctx context.Context, _ *struct{}) (*ParentsOutput, error) {
	items, err := h.parents.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &ParentsOutput{}
	out.Body.Items = items
	return out, nil
}

type SetParentRoleInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Owner bool `json:"owner"`
	}
}

type ParentOutput struct {
	Body parents.Parent
}

func (h *handlers) setParentRole(ctx context.Context, in *SetParentRoleInput) (*ParentOutput, error) {
	if err := h.parents.SetOwner(ctx, in.ID, in.Body.Owner); err != nil {
		return nil, err
	}
	p, err := h.parents.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &ParentOutput{Body: p}, nil
}

func (h *handlers) removeParent(ctx context.Context, in *ParentIDInput) (*struct{}, error) {
	return nil, h.parents.Remove(ctx, in.ID)
}

type TemporaryPasswordOutput struct {
	Body struct {
		Password string `json:"password" doc:"Temporary password to pass on; shown only now"`
	}
}

func (h *handlers) resetParentPassword(ctx context.Context, in *ParentIDInput) (*TemporaryPasswordOutput, error) {
	pw, err := h.parents.ResetPassword(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := &TemporaryPasswordOutput{}
	out.Body.Password = pw
	return out, nil
}

func (h *handlers) disableParentTOTP(ctx context.Context, in *ParentIDInput) (*struct{}, error) {
	return nil, h.parents.DisableTOTP(ctx, in.ID)
}

type CreateInviteInput struct {
	Body struct {
		Role string `json:"role" enum:"parent,owner" default:"parent"`
	}
}

type CreateInviteOutput struct {
	Body struct {
		parents.Invite
		URL string `json:"url" doc:"Link to share or show as a QR code; contains the token, shown only now"`
	}
}

func (h *handlers) createInvite(ctx context.Context, in *CreateInviteInput) (*CreateInviteOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	inv, token, err := h.parents.CreateInvite(ctx, p, in.Body.Role)
	if err != nil {
		return nil, err
	}
	out := &CreateInviteOutput{}
	out.Body.Invite = inv
	out.Body.URL = auth.RequestInfoFrom(ctx).BaseURL(h.cfg.PublicURL) + "/invite?token=" + url.QueryEscape(token)
	return out, nil
}

type InvitesOutput struct {
	Body struct {
		Items []parents.Invite `json:"items" nullable:"false"`
	}
}

func (h *handlers) listInvites(ctx context.Context, _ *struct{}) (*InvitesOutput, error) {
	items, err := h.parents.PendingInvites(ctx)
	if err != nil {
		return nil, err
	}
	out := &InvitesOutput{}
	out.Body.Items = items
	return out, nil
}

type InviteIDInput struct {
	ID int64 `path:"id"`
}

func (h *handlers) revokeInvite(ctx context.Context, in *InviteIDInput) (*struct{}, error) {
	return nil, h.parents.RevokeInvite(ctx, in.ID)
}

type InviteTokenInput struct {
	Body struct {
		Token string `json:"token" minLength:"1" maxLength:"100"`
	}
}

type InviteInfoOutput struct {
	Body struct {
		Role          string    `json:"role" enum:"parent,owner"`
		HouseholdName string    `json:"householdName"`
		InvitedBy     string    `json:"invitedBy,omitempty"`
		ExpiresAt     time.Time `json:"expiresAt"`
		OIDC          bool      `json:"oidc" doc:"Single sign-on is available for joining"`
	}
}

func (h *handlers) getInvite(ctx context.Context, in *InviteTokenInput) (*InviteInfoOutput, error) {
	info, err := h.parents.LookupInvite(ctx, in.Body.Token)
	if err != nil {
		return nil, err
	}
	out := &InviteInfoOutput{}
	out.Body.Role, out.Body.InvitedBy, out.Body.ExpiresAt = info.Role, info.CreatedByName, info.ExpiresAt
	out.Body.OIDC = h.cfg.OIDC.Enabled()
	if hh, err := h.db.GetHousehold(ctx); err == nil {
		out.Body.HouseholdName = hh.Name
	}
	return out, nil
}

type AcceptInviteInput struct {
	Body struct {
		Token       string `json:"token" minLength:"1" maxLength:"100"`
		DisplayName string `json:"displayName" minLength:"1" maxLength:"40"`
		Username    string `json:"username" pattern:"^[a-z0-9][a-z0-9._-]{2,31}$"`
		Password    string `json:"password" minLength:"8" maxLength:"128"`
	}
}

type AcceptInviteOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      struct {
		UserID int64 `json:"userId"`
	}
}

func (h *handlers) acceptInvite(ctx context.Context, in *AcceptInviteInput) (*AcceptInviteOutput, error) {
	hash, err := auth.HashSecret(in.Body.Password)
	if err != nil {
		return nil, err
	}
	id, session, err := h.parents.Accept(ctx, in.Body.Token, parents.NewParent{
		DisplayName: in.Body.DisplayName, Username: in.Body.Username, PasswordHash: hash,
	})
	if err != nil {
		return nil, err
	}
	c := auth.SessionCookieFor(ctx, session)
	out := &AcceptInviteOutput{SetCookie: c.String()}
	out.Body.UserID = id
	return out, nil
}
