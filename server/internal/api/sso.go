package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/sso"
)

func registerSSO(hapi huma.API, h *handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "startOidc",
		Method:      http.MethodGet,
		Path:        "/auth/oidc/start",
		Summary:     "Start single sign-on (browser navigation)",
		Description: "Redirects to the identity provider. `client=web` returns to the admin signed in; `client=app` " +
			"ends at `chorecast://auth?code=…` (or `?error=…`), which the app exchanges with its PKCE verifier at " +
			"/auth/oidc/exchange. Only parents who linked their identity can sign in this way.",
		Tags:          []string{"auth"},
		Metadata:      auth.Public(),
		DefaultStatus: http.StatusFound,
		Errors:        []int{http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway},
	}, h.startOIDC)
	huma.Register(hapi, huma.Operation{
		OperationID:   "oidcCallback",
		Method:        http.MethodGet,
		Path:          "/auth/oidc/callback",
		Summary:       "Identity provider redirect target",
		Description:   "Register `<server URL>" + sso.CallbackPath + "` as the redirect URI with your identity provider.",
		Tags:          []string{"auth"},
		Metadata:      auth.Public(),
		DefaultStatus: http.StatusFound,
	}, h.oidcCallback)
	huma.Register(hapi, huma.Operation{
		OperationID: "exchangeOidcCode",
		Method:      http.MethodPost,
		Path:        "/auth/oidc/exchange",
		Summary:     "Exchange the app's single sign-on code for an app token",
		Tags:        []string{"auth"},
		Metadata:    auth.Public(),
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound},
	}, h.exchangeOIDC)
	huma.Register(hapi, huma.Operation{
		OperationID:   "linkOidc",
		Method:        http.MethodGet,
		Path:          "/account/oidc/link",
		Summary:       "Link your identity provider account (browser navigation)",
		Description:   "Redirects to the identity provider, then back to /account?sso=linked (or ?sso_error=…).",
		Tags:          []string{"account"},
		Security:      auth.Require(auth.ScopeAccountManage),
		DefaultStatus: http.StatusFound,
		Errors:        []int{http.StatusNotFound, http.StatusBadGateway},
	}, h.linkOIDC)
	huma.Register(hapi, huma.Operation{
		OperationID:   "unlinkOidc",
		Method:        http.MethodPost,
		Path:          "/account/oidc/unlink",
		Summary:       "Unlink your identity provider account",
		Tags:          []string{"account"},
		Security:      auth.Require(auth.ScopeAccountManage),
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden, http.StatusConflict},
	}, h.unlinkOIDC)
}

type RedirectOutput struct {
	Location  string   `header:"Location"`
	SetCookie []string `header:"Set-Cookie"`
}

func stateCookie(ctx context.Context, value string, maxAge time.Duration) string {
	c := http.Cookie{ //nolint:gosec // Secure is set per request (HTTPS or not); plain-HTTP LAN installs must work
		Name: sso.StateCookie, Value: value, Path: "/api/v1/auth/oidc", HttpOnly: true,
		Secure: auth.RequestInfoFrom(ctx).HTTPS, SameSite: http.SameSiteLaxMode, MaxAge: int(maxAge / time.Second),
	}
	if maxAge < 0 {
		c.MaxAge = -1
	}
	return c.String()
}

type StartOIDCInput struct {
	// Validated in the handler, not with an enum: openapi-generator's Kotlin client mishandles
	// enum query parameters, and this browser-navigation endpoint isn't called through clients.
	Client    string `query:"client" default:"web" doc:"web, app or invite"`
	Redirect  string `query:"redirect" doc:"web: path to return to after signing in"`
	Challenge string `query:"challenge" doc:"app: base64url(SHA-256(verifier)), 43 characters"`
	Label     string `query:"label" maxLength:"100" doc:"app: device name for the session list"`
	Invite    string `query:"invite" doc:"invite: the invite token"`
	Name      string `query:"displayName" maxLength:"40" doc:"invite: the new parent's name"`
	Username  string `query:"username" maxLength:"32" doc:"invite: optional username for password sign-in later"`
}

func (h *handlers) startOIDC(ctx context.Context, in *StartOIDCInput) (*RedirectOutput, error) {
	switch sso.Client(in.Client) {
	case sso.ClientWeb, sso.ClientApp, sso.ClientInvite:
	default:
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "client must be web, app or invite")
	}
	authURL, state, err := h.sso.Start(ctx, sso.StartInput{
		Client: sso.Client(in.Client), Redirect: in.Redirect, AppChallenge: in.Challenge, AppLabel: in.Label,
		Invite: in.Invite, DisplayName: in.Name, Username: in.Username,
	})
	if err != nil {
		return nil, err
	}
	return &RedirectOutput{Location: authURL, SetCookie: []string{stateCookie(ctx, state, 10*time.Minute)}}, nil
}

type OIDCCallbackInput struct {
	Code        string `query:"code"`
	State       string `query:"state"`
	Error       string `query:"error"`
	Description string `query:"error_description"`
	Bound       string `cookie:"chorecast_oidc"`
}

func (h *handlers) oidcCallback(ctx context.Context, in *OIDCCallbackInput) (*RedirectOutput, error) {
	location, session := h.sso.Callback(ctx, sso.CallbackInput{
		State: in.State, BoundState: in.Bound, Code: in.Code, IdPError: in.Error, Description: in.Description,
	})
	out := &RedirectOutput{Location: location, SetCookie: []string{stateCookie(ctx, "", -1)}}
	if session != "" {
		c := auth.SessionCookieFor(ctx, session)
		out.SetCookie = append(out.SetCookie, c.String())
	}
	return out, nil
}

type ExchangeOIDCInput struct {
	Body struct {
		Code     string `json:"code" minLength:"1" maxLength:"100"`
		Verifier string `json:"verifier" minLength:"43" maxLength:"128" doc:"The PKCE verifier whose challenge started the flow"`
		Label    string `json:"label,omitempty" maxLength:"100"`
	}
}

type ExchangeOIDCOutput struct {
	Body struct {
		UserID int64  `json:"userId"`
		Token  string `json:"token" doc:"Bearer token for the app"`
	}
}

func (h *handlers) exchangeOIDC(ctx context.Context, in *ExchangeOIDCInput) (*ExchangeOIDCOutput, error) {
	token, userID, err := h.sso.Exchange(ctx, in.Body.Code, in.Body.Verifier, in.Body.Label)
	if err != nil {
		return nil, err
	}
	out := &ExchangeOIDCOutput{}
	out.Body.UserID, out.Body.Token = userID, token
	return out, nil
}

func (h *handlers) linkOIDC(ctx context.Context, _ *struct{}) (*RedirectOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	authURL, state, err := h.sso.Start(ctx, sso.StartInput{Client: sso.ClientLink, UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	return &RedirectOutput{Location: authURL, SetCookie: []string{stateCookie(ctx, state, 10*time.Minute)}}, nil
}

func (h *handlers) unlinkOIDC(ctx context.Context, in *PasswordInput) (*struct{}, error) {
	p, _ := auth.PrincipalFrom(ctx)
	if err := h.account.ConfirmPassword(ctx, p, in.Body.Password); err != nil {
		return nil, err
	}
	return nil, h.sso.Unlink(ctx, p.UserID)
}
