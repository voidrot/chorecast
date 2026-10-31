package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/account"
	"github.com/voidrot/chorecast/server/internal/auth"
)

func registerAuth(hapi huma.API, h *handlers) {
	self := auth.Require(auth.ScopeAccountManage)
	huma.Register(hapi, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "Sign in as a parent",
		Description: "Password login. `client: web` sets the session cookie; `client: app` returns a bearer token " +
			"for the Android app. When two-factor sign-in is on, a correct password without a code answers 401 " +
			"`totp_required`: send the same request again with `totpCode` or `recoveryCode`. Repeated failures " +
			"answer 429 with Retry-After.",
		Tags:     []string{"auth"},
		Metadata: auth.Public(),
		Errors:   []int{http.StatusUnauthorized, http.StatusTooManyRequests},
	}, h.login)
	huma.Register(hapi, huma.Operation{
		OperationID:   "logout",
		Method:        http.MethodPost,
		Path:          "/auth/logout",
		Summary:       "Sign out (revokes the calling session or app token)",
		Tags:          []string{"auth"},
		Security:      self,
		DefaultStatus: http.StatusNoContent,
	}, h.logout)

	huma.Register(hapi, huma.Operation{
		OperationID: "listSessions",
		Method:      http.MethodGet,
		Path:        "/account/sessions",
		Summary:     "Your web sessions and app sign-ins",
		Tags:        []string{"account"},
		Security:    self,
	}, h.listSessions)
	huma.Register(hapi, huma.Operation{
		OperationID:   "revokeSession",
		Method:        http.MethodDelete,
		Path:          "/account/sessions/{id}",
		Summary:       "Sign out one of your sessions",
		Tags:          []string{"account"},
		Security:      self,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusNotFound},
	}, h.revokeSession)
	huma.Register(hapi, huma.Operation{
		OperationID:   "changePassword",
		Method:        http.MethodPut,
		Path:          "/account/password",
		Summary:       "Change your password (signs out your other sessions)",
		Tags:          []string{"account"},
		Security:      self,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden},
	}, h.changePassword)
	huma.Register(hapi, huma.Operation{
		OperationID:   "setParentPin",
		Method:        http.MethodPut,
		Path:          "/account/pin",
		Summary:       "Set or clear your parent PIN (for shared tablets)",
		Description:   "An empty `pin` clears it.",
		Tags:          []string{"account"},
		Security:      self,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden, http.StatusUnprocessableEntity},
	}, h.setPIN)
	huma.Register(hapi, huma.Operation{
		OperationID: "beginTotp",
		Method:      http.MethodPost,
		Path:        "/account/totp/setup",
		Summary:     "Start two-factor setup",
		Description: "Returns a new secret and otpauth:// URL (show it as a QR code). Confirm with enableTotp.",
		Tags:        []string{"account"},
		Security:    self,
		Errors:      []int{http.StatusConflict},
	}, h.beginTOTP)
	huma.Register(hapi, huma.Operation{
		OperationID: "enableTotp",
		Method:      http.MethodPost,
		Path:        "/account/totp/enable",
		Summary:     "Turn on two-factor sign-in",
		Description: "Confirms setup with a code from the authenticator app and returns recovery codes, shown only once.",
		Tags:        []string{"account"},
		Security:    self,
		Errors:      []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, h.enableTOTP)
	huma.Register(hapi, huma.Operation{
		OperationID:   "disableTotp",
		Method:        http.MethodPost,
		Path:          "/account/totp/disable",
		Summary:       "Turn off two-factor sign-in",
		Tags:          []string{"account"},
		Security:      self,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden, http.StatusConflict},
	}, h.disableTOTP)
	huma.Register(hapi, huma.Operation{
		OperationID: "regenerateRecoveryCodes",
		Method:      http.MethodPost,
		Path:        "/account/recovery-codes",
		Summary:     "Replace your recovery codes",
		Description: "Old codes stop working. The new ones are shown only once.",
		Tags:        []string{"account"},
		Security:    self,
		Errors:      []int{http.StatusForbidden, http.StatusConflict},
	}, h.regenerateRecoveryCodes)
}

type LoginInput struct {
	Body struct {
		Username     string `json:"username" minLength:"1" maxLength:"64"`
		Password     string `json:"password" minLength:"1" maxLength:"128"`
		TOTPCode     string `json:"totpCode,omitempty" maxLength:"16" doc:"6-digit code from the authenticator app"`
		RecoveryCode string `json:"recoveryCode,omitempty" maxLength:"32" doc:"One of the recovery codes (single use)"`
		Client       string `json:"client" enum:"web,app" doc:"web: session cookie; app: bearer token in the response"`
		Label        string `json:"label,omitempty" maxLength:"100" doc:"Device name shown in the session list (app)"`
	}
}

type LoginOutput struct {
	SetCookie string `header:"Set-Cookie" doc:"Web session cookie (client: web)"`
	Body      struct {
		UserID            int64  `json:"userId"`
		Token             string `json:"token,omitempty" doc:"Bearer token (client: app only)"`
		RecoveryCodesLeft *int64 `json:"recoveryCodesLeft,omitempty" doc:"Set when a recovery code was used"`
	}
}

func (h *handlers) login(ctx context.Context, in *LoginInput) (*LoginOutput, error) {
	res, err := h.account.Login(ctx, account.LoginInput{
		Username: in.Body.Username, Password: in.Body.Password,
		TOTPCode: in.Body.TOTPCode, RecoveryCode: in.Body.RecoveryCode,
		Client: account.Client(in.Body.Client), Label: in.Body.Label,
	})
	if err != nil {
		return nil, err
	}
	out := &LoginOutput{}
	out.Body.UserID = res.UserID
	out.Body.RecoveryCodesLeft = res.RecoveryCodesLeft
	if in.Body.Client == string(account.ClientApp) {
		out.Body.Token = res.Token
	} else {
		c := auth.SessionCookieFor(ctx, res.Token)
		out.SetCookie = c.String()
	}
	return out, nil
}

type LogoutOutput struct {
	SetCookie string `header:"Set-Cookie"`
}

func (h *handlers) logout(ctx context.Context, _ *struct{}) (*LogoutOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	if err := h.account.Logout(ctx, p.TokenID); err != nil {
		return nil, err
	}
	c := auth.ClearSessionCookie(ctx)
	return &LogoutOutput{SetCookie: c.String()}, nil
}

type SessionsOutput struct {
	Body struct {
		Items []account.Session `json:"items" nullable:"false"`
	}
}

func (h *handlers) listSessions(ctx context.Context, _ *struct{}) (*SessionsOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	items, err := h.account.Sessions(ctx, p)
	if err != nil {
		return nil, err
	}
	out := &SessionsOutput{}
	out.Body.Items = items
	return out, nil
}

type RevokeSessionInput struct {
	ID int64 `path:"id"`
}

func (h *handlers) revokeSession(ctx context.Context, in *RevokeSessionInput) (*struct{}, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return nil, h.account.RevokeSession(ctx, p, in.ID)
}

type ChangePasswordInput struct {
	Body struct {
		Current string `json:"currentPassword,omitempty" maxLength:"128" doc:"Required unless you have no password yet (joined with single sign-on)"`
		New     string `json:"newPassword" minLength:"8" maxLength:"128"`
	}
}

func (h *handlers) changePassword(ctx context.Context, in *ChangePasswordInput) (*struct{}, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return nil, h.account.ChangePassword(ctx, p, in.Body.Current, in.Body.New)
}

type SetPINInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"128" doc:"Your account password, to confirm"`
		PIN      string `json:"pin" maxLength:"8" doc:"4-8 digits; empty clears the PIN"`
	}
}

func (h *handlers) setPIN(ctx context.Context, in *SetPINInput) (*struct{}, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return nil, h.account.SetPIN(ctx, p, in.Body.Password, in.Body.PIN)
}

type BeginTOTPOutput struct {
	Body struct {
		Secret     string `json:"secret" doc:"Base32 secret, for typing into an authenticator app"`
		OtpauthURL string `json:"otpauthUrl" doc:"otpauth:// URL to show as a QR code"`
	}
}

func (h *handlers) beginTOTP(ctx context.Context, _ *struct{}) (*BeginTOTPOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	secret, url, err := h.account.BeginTOTP(ctx, p)
	if err != nil {
		return nil, err
	}
	out := &BeginTOTPOutput{}
	out.Body.Secret, out.Body.OtpauthURL = secret, url
	return out, nil
}

type RecoveryCodesOutput struct {
	Body struct {
		RecoveryCodes []string `json:"recoveryCodes" nullable:"false" doc:"Single-use codes for signing in without the authenticator app. Shown only now."`
	}
}

type EnableTOTPInput struct {
	Body struct {
		Code string `json:"code" minLength:"6" maxLength:"16"`
	}
}

func (h *handlers) enableTOTP(ctx context.Context, in *EnableTOTPInput) (*RecoveryCodesOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	codes, err := h.account.EnableTOTP(ctx, p, in.Body.Code)
	if err != nil {
		return nil, err
	}
	out := &RecoveryCodesOutput{}
	out.Body.RecoveryCodes = codes
	return out, nil
}

type PasswordInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"128" doc:"Your account password, to confirm"`
	}
}

func (h *handlers) disableTOTP(ctx context.Context, in *PasswordInput) (*struct{}, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return nil, h.account.DisableTOTP(ctx, p, in.Body.Password)
}

func (h *handlers) regenerateRecoveryCodes(ctx context.Context, in *PasswordInput) (*RecoveryCodesOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	codes, err := h.account.RegenerateRecoveryCodes(ctx, p, in.Body.Password)
	if err != nil {
		return nil, err
	}
	out := &RecoveryCodesOutput{}
	out.Body.RecoveryCodes = codes
	return out, nil
}
