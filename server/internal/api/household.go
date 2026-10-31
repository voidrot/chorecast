package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/version"
)

func registerHousehold(hapi huma.API, h *handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "getHousehold",
		Method:      http.MethodGet,
		Path:        "/household",
		Summary:     "Household settings",
		Tags:        []string{"household"},
		Security:    auth.Require(auth.ScopeHouseholdRead),
	}, h.getHousehold)
	huma.Register(hapi, huma.Operation{
		OperationID: "updateHousehold",
		Method:      http.MethodPut,
		Path:        "/household",
		Summary:     "Change household settings",
		Description: "Replaces all settings. Changing the timezone, day end or week start regenerates untouched " +
			"upcoming chores. The day end can't be earlier than a kid's day-end override.",
		Tags:     []string{"household"},
		Security: auth.Require(auth.ScopeHouseholdManage),
		Errors:   []int{http.StatusUnprocessableEntity},
	}, h.updateHousehold)
	huma.Register(hapi, huma.Operation{
		OperationID: "getServerSettings",
		Method:      http.MethodGet,
		Path:        "/server/settings",
		Summary:     "Server-level settings (owners only)",
		Description: "Settings that come from environment variables are read-only here (`source: environment`).",
		Tags:        []string{"server"},
		Security:    auth.Require(auth.ScopeServerManage),
	}, h.getServerSettings)
}

type HouseholdOutput struct {
	Body household.HouseholdSettings
}

func (h *handlers) getHousehold(ctx context.Context, _ *struct{}) (*HouseholdOutput, error) {
	s, err := h.household.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &HouseholdOutput{Body: s}, nil
}

type UpdateHouseholdInput struct {
	Body household.HouseholdSettings
}

func (h *handlers) updateHousehold(ctx context.Context, in *UpdateHouseholdInput) (*HouseholdOutput, error) {
	s, err := h.household.Update(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &HouseholdOutput{Body: s}, nil
}

// ServerSetting is one server-level setting.
type ServerSetting struct {
	Key    string `json:"key" doc:"Stable identifier, e.g. publicUrl"`
	Env    string `json:"env" doc:"The environment variable that sets it"`
	Value  string `json:"value,omitempty" doc:"Current value; secrets are never returned"`
	Secret bool   `json:"secret,omitempty" doc:"The value is hidden; Value is empty"`
	Source string `json:"source" enum:"environment,default" doc:"environment: read-only here"`
}

type ServerSettingsOutput struct {
	Body struct {
		Version string          `json:"version"`
		Items   []ServerSetting `json:"items" nullable:"false"`
	}
}

func (h *handlers) getServerSettings(_ context.Context, _ *struct{}) (*ServerSettingsOutput, error) {
	c := h.cfg
	item := func(key, env, value string, secret bool) ServerSetting {
		s := ServerSetting{Key: key, Env: env, Value: value, Secret: secret, Source: "default"}
		if c.FromEnv[env] {
			s.Source = "environment"
		}
		if secret {
			s.Value = ""
		}
		return s
	}
	out := &ServerSettingsOutput{}
	out.Body.Version = version.Version
	out.Body.Items = []ServerSetting{
		item("publicUrl", "CHORECAST_PUBLIC_URL", c.PublicURL, false),
		item("dataDir", "CHORECAST_DATA_DIR", c.DataDir, false),
		item("oidcIssuer", "CHORECAST_OIDC_ISSUER", c.OIDC.Issuer, false),
		item("oidcClientId", "CHORECAST_OIDC_CLIENT_ID", c.OIDC.ClientID, false),
		item("oidcClientSecret", "CHORECAST_OIDC_CLIENT_SECRET", "", true),
	}
	return out, nil
}
