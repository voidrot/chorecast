package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// TokenKind is the kind of an issued token (the tokens.kind column).
type TokenKind string

const (
	TokenWebSession    TokenKind = "web_session"    // browser cookie
	TokenApp           TokenKind = "app"            // parent's phone
	TokenDevice        TokenKind = "device"         // kid-bound or shared device
	TokenDeviceSession TokenKind = "device_session" // short-lived kid session or parent elevation on a device
	TokenDisplay       TokenKind = "display"        // family dashboard
	TokenAPI           TokenKind = "api"            // integration key
)

// touchInterval throttles last_used_at writes so every request doesn't write to SQLite.
const touchInterval = time.Minute

// GenerateToken returns a new random opaque token (32 bytes, base64url).
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the stored form of a token. Tokens are high-entropy, so a plain SHA-256
// (no salt, no stretching) is enough and allows lookup by hash.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Store issues and resolves tokens.
type Store struct {
	q     *sqlc.Queries
	clock clock.Clock
}

func NewStore(q *sqlc.Queries, c clock.Clock) *Store { return &Store{q: q, clock: c} }

// IssueParams describes a token to issue.
type IssueParams struct {
	Kind     TokenKind
	UserID   int64 // 0 for shared devices, displays and API keys
	DeviceID int64 // device and device_session tokens: the paired device
	Label    string
	TTL      time.Duration // 0 = no expiry
}

// Issue creates a token and returns its plaintext (shown to the client once) and row id.
func (s *Store) Issue(ctx context.Context, p IssueParams) (string, int64, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", 0, err
	}
	now := s.clock.Now()
	var expires sql.NullString
	if p.TTL > 0 {
		expires = sql.NullString{String: db.Timestamp(now.Add(p.TTL)), Valid: true}
	}
	row, err := s.q.CreateToken(ctx, sqlc.CreateTokenParams{
		TokenHash: HashToken(token),
		Kind:      string(p.Kind),
		UserID:    sql.NullInt64{Int64: p.UserID, Valid: p.UserID != 0},
		DeviceID:  sql.NullInt64{Int64: p.DeviceID, Valid: p.DeviceID != 0},
		Label:     p.Label,
		CreatedAt: db.Timestamp(now),
		ExpiresAt: expires,
	})
	if err != nil {
		return "", 0, fmt.Errorf("issue token: %w", err)
	}
	return token, row.ID, nil
}

// Authenticate resolves a presented token to a principal. It returns (nil, nil) for unknown,
// expired or mismatched tokens. allowed limits which token kinds this transport accepts
// (cookies only carry web sessions; bearer headers carry everything else).
func (s *Store) Authenticate(ctx context.Context, token string, allowed ...TokenKind) (*Principal, error) {
	if token == "" {
		return nil, nil
	}
	row, err := s.q.ResolveToken(ctx, HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve token: %w", err)
	}
	kind := TokenKind(row.Kind)
	if !kindAllowed(kind, allowed) {
		return nil, nil
	}
	now := s.clock.Now()
	if row.ExpiresAt.Valid {
		exp, err := db.ParseTimestamp(row.ExpiresAt.String)
		if err != nil || !now.Before(exp) {
			return nil, nil
		}
	}

	p := principalFor(kind, row)
	if p == nil {
		return nil, nil
	}
	if err := s.touch(ctx, row, now); err != nil {
		return nil, err
	}
	return p, nil
}

// principalFor maps a token and its user to a principal, rejecting combinations that should
// never exist (e.g. a device token bound to a parent).
func principalFor(kind TokenKind, row sqlc.ResolveTokenRow) *Principal {
	role, userID, owner := row.Role.String, row.UserID.Int64, row.IsOwner.Int64 == 1
	onDevice := func(p *Principal) *Principal {
		p.DeviceID = row.DeviceID.Int64
		return p
	}
	switch kind {
	case TokenWebSession, TokenApp:
		if role == "parent" {
			return newPrincipal(KindParent, userID, owner, row.ID)
		}
	case TokenDevice:
		switch {
		case !row.UserID.Valid:
			return onDevice(newPrincipal(KindSharedDevice, 0, false, row.ID))
		case role == "child":
			return onDevice(newPrincipal(KindKid, userID, false, row.ID))
		}
	case TokenDeviceSession:
		switch role {
		case "child":
			return onDevice(newPrincipal(KindKid, userID, false, row.ID))
		case "parent":
			p := onDevice(newPrincipal(KindParent, userID, false, row.ID))
			p.Elevated = true
			p.setScopes(elevatedParentScopes)
			return p
		}
	case TokenDisplay:
		return newPrincipal(KindDisplay, 0, false, row.ID)
	case TokenAPI:
		return newPrincipal(KindAPI, 0, false, row.ID)
	}
	return nil
}

func kindAllowed(k TokenKind, allowed []TokenKind) bool {
	for _, a := range allowed {
		if k == a {
			return true
		}
	}
	return false
}

func (s *Store) touch(ctx context.Context, row sqlc.ResolveTokenRow, now time.Time) error {
	if row.LastUsedAt.Valid {
		if last, err := db.ParseTimestamp(row.LastUsedAt.String); err == nil && now.Sub(last) < touchInterval {
			return nil
		}
	}
	return s.q.TouchToken(ctx, sqlc.TouchTokenParams{
		LastUsedAt: sql.NullString{String: db.Timestamp(now), Valid: true},
		ID:         row.ID,
	})
}

// Revoke deletes a token.
func (s *Store) Revoke(ctx context.Context, tokenID int64) error {
	return s.q.DeleteToken(ctx, tokenID)
}

// RevokeAllForUser deletes every token belonging to a user (logout everywhere, removed parent).
func (s *Store) RevokeAllForUser(ctx context.Context, userID int64) error {
	return s.q.DeleteTokensForUser(ctx, sql.NullInt64{Int64: userID, Valid: true})
}

// PurgeExpired deletes expired tokens and returns how many were removed.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	return s.q.DeleteExpiredTokens(ctx, sql.NullString{String: db.Timestamp(s.clock.Now()), Valid: true})
}
