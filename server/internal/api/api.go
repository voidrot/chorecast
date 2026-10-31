// Package api defines the HTTP API. Handlers are registered with Huma, which
// generates the OpenAPI spec (code-first) consumed by the web and Android clients.
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/voidrot/chorecast/server/internal/account"
	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/devices"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/idempotency"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/parents"
	"github.com/voidrot/chorecast/server/internal/setup"
	"github.com/voidrot/chorecast/server/internal/sso"
	"github.com/voidrot/chorecast/server/internal/version"
)

const Prefix = "/api/v1"

// Deps are the API's dependencies. DB and Setup are nil when only the spec is needed
// (e.g. `chorecast openapi`).
type Deps struct {
	Config config.Config
	DB     *db.DB
	Clock  clock.Clock
	Setup  *setup.Service
	Bus    *events.Bus // nil in tests that don't need domain events
}

// New registers all API operations on r under Prefix and returns the Huma API.
// It panics if an operation declares no access policy (see auth.CheckOperations).
func New(r chi.Router, deps Deps) huma.API {
	hcfg := huma.DefaultConfig("Chorecast API", version.Version)
	hcfg.Info.Description = "Self-hosted chore and behavior system."
	hcfg.Servers = []*huma.Server{{URL: Prefix}}
	hcfg.OpenAPIPath = "/openapi"
	hcfg.DocsPath = "/docs"
	hcfg.SchemasPath = "/schemas"
	hcfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer":  {Type: "http", Scheme: "bearer", Description: "Opaque app/device token"},
		"session": {Type: "apiKey", In: "cookie", Name: "chorecast_session"},
	}
	// No "$schema" links in responses: they bloat every body and generated clients can't decode them.
	hcfg.CreateHooks = nil
	hcfg.OnAddOperation = append(hcfg.OnAddOperation, idempotency.AddHeaderParam)

	var hapi huma.API
	r.Route(Prefix, func(r chi.Router) {
		r.Use(auth.CaptureRequestInfo)
		hapi = humachi.New(r, hcfg)
		// Unknown paths and methods under the API prefix answer in the same problem format.
		r.NotFound(apierr.Handler(http.StatusNotFound).ServeHTTP)
		r.MethodNotAllowed(apierr.Handler(http.StatusMethodNotAllowed).ServeHTTP)
	})

	var queries *sqlc.Queries
	if deps.DB != nil {
		queries = deps.DB.Queries
	}
	// Order matters: the setup gate runs first; idempotency needs the principal resolved by auth.
	hapi.UseMiddleware(
		setup.Gate(hapi, deps.Setup),
		auth.Middleware(hapi, auth.NewStore(queries, deps.Clock)),
		idempotency.Middleware(hapi, queries, deps.Clock),
	)

	h := &handlers{cfg: deps.Config, db: deps.DB, setup: deps.Setup, account: account.New(deps.DB, deps.Clock),
		sso: sso.New(deps.Config, deps.DB, deps.Clock), parents: parents.New(deps.DB, deps.Clock),
		kids: kids.NewService(deps.DB, deps.Clock), household: household.New(deps.DB, deps.Bus),
		devices: devices.New(deps.DB, deps.Clock)}
	huma.Register(hapi, huma.Operation{
		OperationID: "getHealth",
		Method:      "GET",
		Path:        "/health",
		Summary:     "Liveness and readiness check",
		Tags:        []string{"system"},
		Metadata:    setup.AllowDuringSetup(auth.Public()),
	}, h.health)
	huma.Register(hapi, huma.Operation{
		OperationID: "getVersion",
		Method:      "GET",
		Path:        "/version",
		Summary:     "Server and API version (used by clients for compatibility checks)",
		Tags:        []string{"system"},
		Metadata:    setup.AllowDuringSetup(auth.Public()),
	}, h.version)
	registerSetup(hapi, h)
	registerAuth(hapi, h)
	registerSSO(hapi, h)
	registerParents(hapi, h)
	registerKids(hapi, h)
	registerHousehold(hapi, h)
	registerDevices(hapi, h)

	huma.Register(hapi, huma.Operation{
		OperationID: "getMe",
		Method:      "GET",
		Path:        "/me",
		Summary:     "The authenticated caller: kind, user and granted scopes",
		Tags:        []string{"auth"},
		Security:    auth.Require(), // any authenticated caller
	}, h.me)

	if err := auth.CheckOperations(hapi.OpenAPI()); err != nil {
		panic(err)
	}
	return hapi
}

type handlers struct {
	cfg       config.Config
	db        *db.DB
	setup     *setup.Service
	account   *account.Service
	sso       *sso.Service
	parents   *parents.Service
	kids      *kids.Service
	household *household.Service
	devices   *devices.Service
}

type HealthOutput struct {
	Body struct {
		Status string `json:"status" enum:"ok,degraded"`
	}
}

func (h *handlers) health(ctx context.Context, _ *struct{}) (*HealthOutput, error) {
	out := &HealthOutput{}
	out.Body.Status = "ok"
	if err := h.db.SQL.PingContext(ctx); err != nil {
		out.Body.Status = "degraded"
	}
	return out, nil
}

type VersionOutput struct {
	Body struct {
		Version    string `json:"version" example:"0.1.0"`
		Commit     string `json:"commit"`
		APIVersion int    `json:"apiVersion" example:"1"`
		OIDC       bool   `json:"oidc" doc:"Whether SSO login is available"`
	}
}

func (h *handlers) version(_ context.Context, _ *struct{}) (*VersionOutput, error) {
	out := &VersionOutput{}
	out.Body.Version = version.Version
	out.Body.Commit = version.Commit
	out.Body.APIVersion = version.APIVersion
	out.Body.OIDC = h.cfg.OIDC.Enabled()
	return out, nil
}

type MeOutput struct {
	Body struct {
		Kind   string   `json:"kind" enum:"parent,kid,shared_device,display,api" doc:"What kind of caller this is"`
		UserID int64    `json:"userId,omitempty" doc:"The parent or kid; absent for shared devices, displays and API keys"`
		Owner  bool     `json:"owner" doc:"Parent with owner rights (server settings)"`
		Scopes []string `json:"scopes" nullable:"false" doc:"Granted scopes, for clients to hide what the caller can't do"`

		DisplayName string `json:"displayName,omitempty" doc:"Parents and kids"`
		Username    string `json:"username,omitempty" doc:"Parents only"`
		TOTPEnabled bool   `json:"totpEnabled" doc:"Parents: two-factor sign-in is on"`
		PINSet      bool   `json:"pinSet" doc:"Parents: a parent PIN is set"`
		OIDCLinked  bool   `json:"oidcLinked" doc:"Parents: an identity provider account is linked for single sign-on"`
		HasPassword bool   `json:"hasPassword" doc:"Parents: false for parents who only use single sign-on"`
		DeviceID    int64  `json:"deviceId,omitempty" doc:"The paired device this request comes from"`
		Elevated    bool   `json:"elevated,omitempty" doc:"A parent elevated on a device with their parent PIN"`
	}
}

func (h *handlers) me(ctx context.Context, _ *struct{}) (*MeOutput, error) {
	p, _ := auth.PrincipalFrom(ctx) // guaranteed by the operation's security policy
	out := &MeOutput{}
	out.Body.Kind = string(p.Kind)
	out.Body.UserID = p.UserID
	out.Body.Owner = p.Owner
	out.Body.DeviceID = p.DeviceID
	out.Body.Elevated = p.Elevated
	out.Body.Scopes = make([]string, 0)
	for _, s := range p.Scopes() {
		out.Body.Scopes = append(out.Body.Scopes, string(s))
	}
	if p.UserID != 0 {
		u, err := h.db.GetUser(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		out.Body.DisplayName = u.DisplayName
		if p.Kind == auth.KindParent {
			out.Body.Username = u.Username.String
			out.Body.TOTPEnabled = u.TotpEnabled == 1
			out.Body.PINSet = u.ParentPinHash.Valid
			out.Body.OIDCLinked = u.OidcSubject.Valid
			out.Body.HasPassword = u.PasswordHash.Valid
		}
	}
	return out, nil
}
