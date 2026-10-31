package auth

import (
	"context"
	"slices"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	Kind    Kind
	UserID  int64 // the parent or kid; 0 for shared devices, displays and API keys
	Owner   bool  // parent with owner rights
	TokenID int64
	scopes  map[Scope]struct{}
}

func newPrincipal(kind Kind, userID int64, owner bool, tokenID int64) *Principal {
	p := &Principal{Kind: kind, UserID: userID, Owner: owner, TokenID: tokenID, scopes: map[Scope]struct{}{}}
	for _, s := range scopesFor(kind, owner) {
		p.scopes[s] = struct{}{}
	}
	return p
}

// Has reports whether the principal holds scope s.
func (p *Principal) Has(s Scope) bool {
	_, ok := p.scopes[s]
	return ok
}

// HasAll reports whether the principal holds every scope in ss.
func (p *Principal) HasAll(ss []Scope) bool {
	for _, s := range ss {
		if !p.Has(s) {
			return false
		}
	}
	return true
}

// Scopes returns the principal's scopes, sorted.
func (p *Principal) Scopes() []Scope {
	out := make([]Scope, 0, len(p.scopes))
	for s := range p.scopes {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// CanAccessKid reports whether the principal may see or act on kid kidID's data (scopes still
// decide which actions). Parents and family displays cover every kid; a kid only themselves.
func (p *Principal) CanAccessKid(kidID int64) bool {
	switch p.Kind {
	case KindParent, KindDisplay:
		return true
	case KindKid:
		return p.UserID == kidID
	default:
		return false
	}
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the request's principal, if authenticated.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}
