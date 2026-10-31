package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/setup"
)

func registerSetup(hapi huma.API, h *handlers) {
	meta := setup.AllowDuringSetup(auth.Public())
	huma.Register(hapi, huma.Operation{
		OperationID: "getSetupStatus",
		Method:      http.MethodGet,
		Path:        "/setup",
		Summary:     "Whether first-run setup is still required",
		Tags:        []string{"setup"},
		Metadata:    meta,
	}, h.setupStatus)
	huma.Register(hapi, huma.Operation{
		OperationID:   "verifySetupToken",
		Method:        http.MethodPost,
		Path:          "/setup/verify",
		Summary:       "Check a setup token without using it",
		Description:   "Lets the wizard reject a stale setup link before any forms are filled in.",
		Tags:          []string{"setup"},
		Metadata:      meta,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden, http.StatusConflict},
	}, h.verifySetup)
	huma.Register(hapi, huma.Operation{
		OperationID: "completeSetup",
		Method:      http.MethodPost,
		Path:        "/setup",
		Summary:     "Complete first-run setup",
		Description: "Creates the household, the first owner and any kids in one transaction, invalidates the " +
			"setup token and signs the owner in (web session cookie).",
		Tags:          []string{"setup"},
		Metadata:      meta,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity},
	}, h.completeSetup)
}

type SetupStatusOutput struct {
	Body struct {
		Required bool `json:"required" doc:"True until first-run setup completes"`
		OIDC     bool `json:"oidc" doc:"Whether SSO is configured (the owner may sign in with it later)"`
	}
}

func (h *handlers) setupStatus(_ context.Context, _ *struct{}) (*SetupStatusOutput, error) {
	out := &SetupStatusOutput{}
	out.Body.Required = h.setup.Required()
	out.Body.OIDC = h.cfg.OIDC.Enabled()
	return out, nil
}

type VerifySetupInput struct {
	Body struct {
		Token string `json:"token" minLength:"1"`
	}
}

func (h *handlers) verifySetup(ctx context.Context, in *VerifySetupInput) (*struct{}, error) {
	return nil, h.setup.Verify(ctx, in.Body.Token)
}

type CompleteSetupInput struct {
	Body setup.SetupRequest
}

type CompleteSetupOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		UserID int64 `json:"userId" doc:"The new owner"`
	}
}

func (h *handlers) completeSetup(ctx context.Context, in *CompleteSetupInput) (*CompleteSetupOutput, error) {
	res, err := h.setup.Complete(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	out := &CompleteSetupOutput{SetCookie: auth.SessionCookieFor(ctx, res.SessionToken)}
	out.Body.UserID = res.OwnerID
	return out, nil
}
