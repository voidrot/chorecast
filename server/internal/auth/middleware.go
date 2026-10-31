package auth

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// SessionCookie carries the web admin's session token.
const SessionCookie = "chorecast_session"

const metaPublic = "chorecast:public"

// Require is an operation's Security value: callers need every listed scope, via either a
// bearer token or the web session cookie. Use it as huma.Operation.Security.
func Require(scopes ...Scope) []map[string][]string {
	s := make([]string, len(scopes))
	for i, sc := range scopes {
		s[i] = string(sc)
	}
	return []map[string][]string{{"bearer": s}, {"session": s}}
}

// Public marks an operation as callable without authentication. Use it as
// huma.Operation.Metadata.
func Public() map[string]any { return map[string]any{metaPublic: true} }

func isPublic(op *huma.Operation) bool {
	v, _ := op.Metadata[metaPublic].(bool)
	return v
}

// requiredScopes returns the scopes an operation declares, and whether it declares any policy.
func requiredScopes(op *huma.Operation) ([]Scope, bool) {
	if len(op.Security) == 0 {
		return nil, false
	}
	// Every alternative carries the same scopes (see Require), so the first one is enough.
	var out []Scope
	for _, ss := range op.Security[0] {
		for _, s := range ss {
			out = append(out, Scope(s))
		}
	}
	return out, true
}

// Middleware authenticates each request and enforces the operation's declared scopes. An
// operation with no policy (neither Require nor Public) is refused, so forgetting to declare
// one fails closed.
func Middleware(api huma.API, store *Store) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		p, err := authenticate(ctx, store)
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "authentication failed", err)
			return
		}
		if p != nil {
			ctx = huma.WithContext(ctx, WithPrincipal(ctx.Context(), p))
		}
		if isPublic(op) {
			next(ctx)
			return
		}
		scopes, declared := requiredScopes(op)
		switch {
		case !declared:
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "operation has no access policy")
		case p == nil:
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication required")
		case !p.HasAll(scopes):
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "insufficient permissions")
		default:
			next(ctx)
		}
	}
}

// authenticate reads a bearer token (app, device, display, API key) or, failing that, the
// session cookie (web sessions only).
func authenticate(ctx huma.Context, store *Store) (*Principal, error) {
	if h := ctx.Header("Authorization"); h != "" {
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return nil, nil
		}
		return store.Authenticate(ctx.Context(), strings.TrimSpace(token), TokenApp, TokenDevice, TokenDisplay, TokenAPI)
	}
	if c, err := huma.ReadCookie(ctx, SessionCookie); err == nil {
		return store.Authenticate(ctx.Context(), c.Value, TokenWebSession)
	}
	return nil, nil
}

// CheckOperations returns an error naming every operation in the spec that declares neither
// required scopes (Require) nor Public. Called at startup and in tests.
func CheckOperations(oapi *huma.OpenAPI) error {
	var missing []string
	for path, item := range oapi.Paths {
		for method, op := range map[string]*huma.Operation{
			"GET": item.Get, "PUT": item.Put, "POST": item.Post, "DELETE": item.Delete,
			"OPTIONS": item.Options, "HEAD": item.Head, "PATCH": item.Patch, "TRACE": item.Trace,
		} {
			if op == nil || isPublic(op) {
				continue
			}
			if _, ok := requiredScopes(op); !ok {
				missing = append(missing, fmt.Sprintf("%s %s (%s)", method, path, op.OperationID))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("operations without an access policy (use auth.Require or auth.Public): %s",
			strings.Join(missing, ", "))
	}
	return nil
}
