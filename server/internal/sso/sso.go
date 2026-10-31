// Package sso implements optional OpenID Connect sign-in for parents (Authentik, Authelia,
// Keycloak, PocketID...). The server is the only OIDC client: it runs the authorization code flow
// with PKCE for the web admin, for linking an account, and on behalf of the Android app, which
// receives a one-time code on its chorecast:// redirect and exchanges it (with its own PKCE
// verifier) for an app token. Parents are matched by their linked subject; nobody is created
// automatically. The IdP is responsible for second factors, so SSO sign-in skips Chorecast's TOTP.
package sso

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

const (
	// CallbackPath is registered with the IdP as the redirect URI (under the server's base URL).
	CallbackPath = "/api/v1/auth/oidc/callback"
	// AppRedirect is where the Android app receives the one-time code.
	AppRedirect = "chorecast://auth"
	// StateCookie binds a flow to the browser that started it (prevents login CSRF).
	StateCookie = "chorecast_oidc"

	flowTTL     = 10 * time.Minute
	exchangeTTL = 2 * time.Minute
)

// Client is who started a flow.
type Client string

const (
	ClientWeb  Client = "web"  // sign in to the web admin
	ClientApp  Client = "app"  // sign in the Android app
	ClientLink Client = "link" // a signed-in parent links their IdP identity
)

// Error codes passed back to the UI on redirect (?sso_error= or chorecast://auth?error=).
const (
	ErrNotLinked    = "not_linked"     // no parent has this identity linked
	ErrAlreadyUsed  = "already_linked" // another parent has this identity linked
	ErrExpired      = "expired"        // unknown or expired state (took too long, or server restarted)
	ErrDenied       = "denied"         // the IdP returned an error (user cancelled, no access...)
	ErrVerification = "failed"         // token exchange or ID token verification failed
)

type flow struct {
	client       Client
	verifier     string // our PKCE verifier towards the IdP
	nonce        string
	redirect     string // web: path to return to
	appChallenge string // app: S256 challenge from the app
	appLabel     string
	userID       int64 // link: the parent linking
	expires      time.Time
}

type exchange struct {
	userID    int64
	challenge string
	label     string
	expires   time.Time
}

// Service runs OIDC flows. Pending flows and exchange codes live in memory: a restart only
// cancels sign-ins in progress.
type Service struct {
	cfg    config.OIDCConfig
	public string // CHORECAST_PUBLIC_URL, may be empty
	db     *db.DB
	tokens *auth.Store
	clock  clock.Clock

	mu        sync.Mutex
	provider  *oidc.Provider
	flows     map[string]flow
	exchanges map[string]exchange
}

func New(cfg config.Config, d *db.DB, c clock.Clock) *Service {
	var q *sqlc.Queries
	if d != nil {
		q = d.Queries
	}
	return &Service{
		cfg: cfg.OIDC, public: cfg.PublicURL, db: d, tokens: auth.NewStore(q, c), clock: c,
		flows: map[string]flow{}, exchanges: map[string]exchange{},
	}
}

// Enabled reports whether SSO is configured.
func (s *Service) Enabled() bool { return s.cfg.Enabled() }

var errDisabled = apierr.New(http.StatusNotFound, apierr.CodeOIDCDisabled, "single sign-on isn't configured on this server")

// providerFor discovers the IdP on first use and caches it, so a down IdP doesn't stop startup.
func (s *Service) providerFor(ctx context.Context) (*oidc.Provider, error) {
	s.mu.Lock()
	p := s.provider
	s.mu.Unlock()
	if p != nil {
		return p, nil
	}
	// go-oidc detaches this context's cancellation for later JWKS refreshes
	// (TestSSOProviderOutlivesFirstRequest guards that), so the request's context is fine here.
	p, err := oidc.NewProvider(ctx, s.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer: %w", err)
	}
	s.mu.Lock()
	s.provider = p
	s.mu.Unlock()
	return p, nil
}

func (s *Service) oauth(p *oidc.Provider, base string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: s.cfg.ClientID, ClientSecret: s.cfg.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: strings.TrimRight(base, "/") + CallbackPath,
		Scopes: []string{oidc.ScopeOpenID, "profile"},
	}
}

// StartInput describes a new flow.
type StartInput struct {
	Client       Client
	Redirect     string // web: path to return to after sign-in
	AppChallenge string // app: base64url(SHA-256(verifier))
	AppLabel     string // app: device name for the session list
	UserID       int64  // link
}

// Start registers a flow and returns the IdP authorization URL and the state to bind to the
// browser with StateCookie.
func (s *Service) Start(ctx context.Context, in StartInput) (authURL, state string, err error) {
	if !s.Enabled() {
		return "", "", errDisabled
	}
	if in.Client == ClientApp && !validChallenge(in.AppChallenge) {
		return "", "", apierr.New(http.StatusBadRequest, apierr.CodeBadRequest, "challenge must be a base64url SHA-256 (43 characters)")
	}
	p, err := s.providerFor(ctx)
	if err != nil {
		return "", "", apierr.New(http.StatusBadGateway, apierr.CodeUnavailable, "the sign-on provider can't be reached", err)
	}
	if state, err = auth.GenerateToken(); err != nil {
		return "", "", err
	}
	nonce, err := auth.GenerateToken()
	if err != nil {
		return "", "", err
	}
	f := flow{
		client: in.Client, verifier: oauth2.GenerateVerifier(), nonce: nonce, redirect: safePath(in.Redirect),
		appChallenge: in.AppChallenge, appLabel: in.AppLabel, userID: in.UserID, expires: s.clock.Now().Add(flowTTL),
	}
	s.mu.Lock()
	s.sweep()
	s.flows[state] = f
	s.mu.Unlock()
	base := auth.RequestInfoFrom(ctx).BaseURL(s.public)
	return s.oauth(p, base).AuthCodeURL(state, oauth2.S256ChallengeOption(f.verifier), oidc.Nonce(nonce)), state, nil
}

// CallbackInput is what the IdP redirected back with.
type CallbackInput struct {
	State       string
	BoundState  string // from StateCookie
	Code        string
	IdPError    string
	Description string
}

// Callback completes a flow and returns where to send the browser, plus a web session token to
// set as the session cookie (web flows only).
func (s *Service) Callback(ctx context.Context, in CallbackInput) (location, sessionToken string) {
	s.mu.Lock()
	f, ok := s.flows[in.State]
	delete(s.flows, in.State)
	s.mu.Unlock()
	if !ok || in.State == "" || subtle.ConstantTimeCompare([]byte(in.State), []byte(in.BoundState)) != 1 ||
		!s.clock.Now().Before(f.expires) {
		return failURL(ClientWeb, "", ErrExpired), ""
	}
	if in.IdPError != "" {
		return failURL(f.client, f.redirect, ErrDenied), ""
	}
	subject, err := s.verify(ctx, f, in.Code)
	if err != nil {
		return failURL(f.client, f.redirect, ErrVerification), ""
	}

	if f.client == ClientLink {
		return s.link(ctx, f.userID, subject), ""
	}
	user, err := s.db.GetParentByOIDCSubject(ctx, sql.NullString{String: subject, Valid: true})
	if err != nil {
		return failURL(f.client, f.redirect, ErrNotLinked), ""
	}
	switch f.client {
	case ClientApp:
		code, err := auth.GenerateToken()
		if err != nil {
			return failURL(f.client, "", ErrVerification), ""
		}
		s.mu.Lock()
		s.exchanges[code] = exchange{userID: user.ID, challenge: f.appChallenge, label: f.appLabel, expires: s.clock.Now().Add(exchangeTTL)}
		s.mu.Unlock()
		return AppRedirect + "?code=" + url.QueryEscape(code), ""
	default:
		token, _, err := s.tokens.Issue(ctx, auth.IssueParams{
			Kind: auth.TokenWebSession, UserID: user.ID, TTL: auth.WebSessionTTL, Label: "Single sign-on",
		})
		if err != nil {
			return failURL(f.client, f.redirect, ErrVerification), ""
		}
		return f.redirect, token
	}
}

// verify exchanges the code with the IdP and returns the ID token's subject.
func (s *Service) verify(ctx context.Context, f flow, code string) (string, error) {
	p, err := s.providerFor(ctx)
	if err != nil {
		return "", err
	}
	base := auth.RequestInfoFrom(ctx).BaseURL(s.public)
	tok, err := s.oauth(p, base).Exchange(ctx, code, oauth2.VerifierOption(f.verifier))
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return "", errors.New("no id_token in token response")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: s.cfg.ClientID, Now: s.clock.Now}).Verify(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("verify id_token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.nonce)) != 1 {
		return "", errors.New("nonce mismatch")
	}
	if idt.Subject == "" {
		return "", errors.New("empty subject")
	}
	return idt.Subject, nil
}

func (s *Service) link(ctx context.Context, userID int64, subject string) string {
	if other, err := s.db.GetParentByOIDCSubject(ctx, sql.NullString{String: subject, Valid: true}); err == nil {
		if other.ID == userID {
			return "/account?sso=linked"
		}
		return failURL(ClientLink, "", ErrAlreadyUsed)
	}
	if err := s.db.SetOIDCSubject(ctx, sqlc.SetOIDCSubjectParams{OidcSubject: sql.NullString{String: subject, Valid: true}, ID: userID}); err != nil {
		return failURL(ClientLink, "", ErrVerification)
	}
	return "/account?sso=linked"
}

// Exchange trades the app's one-time code and PKCE verifier for an app token.
func (s *Service) Exchange(ctx context.Context, code, verifier, label string) (token string, userID int64, err error) {
	if !s.Enabled() {
		return "", 0, errDisabled
	}
	s.mu.Lock()
	e, ok := s.exchanges[code]
	delete(s.exchanges, code) // single use, even when the verifier is wrong
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	if !ok || code == "" || !s.clock.Now().Before(e.expires) || subtle.ConstantTimeCompare([]byte(got), []byte(e.challenge)) != 1 {
		return "", 0, apierr.New(http.StatusBadRequest, apierr.CodeOIDCInvalidCode, "the sign-in code is invalid or expired; try again")
	}
	if label == "" {
		label = e.label
	}
	if label == "" {
		label = "Android app"
	}
	token, _, err = s.tokens.Issue(ctx, auth.IssueParams{Kind: auth.TokenApp, UserID: e.userID, Label: label})
	return token, e.userID, err
}

// Unlink removes a parent's linked identity. Parents without a password can't unlink (they'd be
// locked out); they set a password first.
func (s *Service) Unlink(ctx context.Context, userID int64) error {
	u, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if !u.OidcSubject.Valid {
		return apierr.New(http.StatusConflict, apierr.CodeOIDCNotLinked, "no single sign-on identity is linked")
	}
	return s.db.SetOIDCSubject(ctx, sqlc.SetOIDCSubjectParams{ID: userID})
}

// sweep drops expired flows and codes. Callers hold mu.
func (s *Service) sweep() {
	now := s.clock.Now()
	for k, f := range s.flows {
		if !now.Before(f.expires) {
			delete(s.flows, k)
		}
	}
	for k, e := range s.exchanges {
		if !now.Before(e.expires) {
			delete(s.exchanges, k)
		}
	}
}

func failURL(c Client, redirect, code string) string {
	switch c {
	case ClientApp:
		return AppRedirect + "?error=" + code
	case ClientLink:
		return "/account?sso_error=" + code
	default:
		q := url.Values{"sso_error": {code}}
		if redirect != "" && redirect != "/" {
			q.Set("redirect", redirect)
		}
		return "/login?" + q.Encode()
	}
}

// safePath only allows same-app paths, never another origin.
func safePath(p string) string {
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.Contains(p, `\`) {
		return p
	}
	return "/"
}

func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}
