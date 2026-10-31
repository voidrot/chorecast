// Package api defines the HTTP API. Handlers are registered with Huma, which
// generates the OpenAPI spec (code-first) consumed by the web and Android clients.
package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/version"
)

const Prefix = "/api/v1"

// New registers all API operations on r under Prefix and returns the Huma API.
// store may be nil when only the spec is needed (e.g. `chorecast openapi`).
// It panics if an operation declares no access policy (see auth.CheckOperations).
func New(r chi.Router, cfg config.Config, store *db.DB, clk clock.Clock) huma.API {
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

	var hapi huma.API
	r.Route(Prefix, func(r chi.Router) {
		hapi = humachi.New(r, hcfg)
	})

	var queries *sqlc.Queries
	if store != nil {
		queries = store.Queries
	}
	hapi.UseMiddleware(auth.Middleware(hapi, auth.NewStore(queries, clk)))

	h := &handlers{cfg: cfg, db: store}
	huma.Register(hapi, huma.Operation{
		OperationID: "getHealth",
		Method:      "GET",
		Path:        "/health",
		Summary:     "Liveness and readiness check",
		Tags:        []string{"system"},
		Metadata:    auth.Public(),
	}, h.health)
	huma.Register(hapi, huma.Operation{
		OperationID: "getVersion",
		Method:      "GET",
		Path:        "/version",
		Summary:     "Server and API version (used by clients for compatibility checks)",
		Tags:        []string{"system"},
		Metadata:    auth.Public(),
	}, h.version)

	if err := auth.CheckOperations(hapi.OpenAPI()); err != nil {
		panic(err)
	}
	return hapi
}

type handlers struct {
	cfg config.Config
	db  *db.DB
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
