// Package parents manages the household's adults: invite links (single use, 7 days), owner and
// parent roles (at least one owner always remains), removing a parent (their row stays so history
// still names them), and owner-driven recovery (temporary password, turning off 2FA). The CLI
// uses the same recovery functions. See docs/features/household.md (Roles & permissions, Parents).
package parents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// InviteTTL is how long an invite link works.
const InviteTTL = 7 * 24 * time.Hour

// UsernamePattern matches valid parent usernames (same rule as first-run setup).
var UsernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

var (
	errInvalidInvite = apierr.New(http.StatusGone, apierr.CodeInvalidInvite,
		"this invite link isn't valid any more (used, revoked or expired); ask for a new one")
	errNotFound  = apierr.New(http.StatusNotFound, apierr.CodeParentNotFound, "no such parent")
	errLastOwner = apierr.New(http.StatusConflict, apierr.CodeLastOwner, "the household must keep at least one owner")
	errTaken     = apierr.New(http.StatusConflict, apierr.CodeUsernameTaken, "that username is taken")
)

type Service struct {
	db    *db.DB
	clock clock.Clock
}

func New(d *db.DB, c clock.Clock) *Service { return &Service{db: d, clock: c} }

// Parent is a parent as listed to other parents.
type Parent struct {
	ID          int64     `json:"id"`
	DisplayName string    `json:"displayName"`
	Username    string    `json:"username,omitempty" doc:"Absent for parents who only use single sign-on"`
	Owner       bool      `json:"owner"`
	TOTPEnabled bool      `json:"totpEnabled"`
	OIDCLinked  bool      `json:"oidcLinked"`
	HasPassword bool      `json:"hasPassword"`
	CreatedAt   time.Time `json:"createdAt"`
}

func toParent(u sqlc.User) Parent {
	created, _ := db.ParseTimestamp(u.CreatedAt)
	return Parent{
		ID: u.ID, DisplayName: u.DisplayName, Username: u.Username.String, Owner: u.IsOwner == 1,
		TOTPEnabled: u.TotpEnabled == 1, OIDCLinked: u.OidcSubject.Valid, HasPassword: u.PasswordHash.Valid,
		CreatedAt: created,
	}
}

func (s *Service) List(ctx context.Context) ([]Parent, error) {
	rows, err := s.db.ListParents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Parent, 0, len(rows))
	for _, u := range rows {
		out = append(out, toParent(u))
	}
	return out, nil
}

// Get returns one parent.
func (s *Service) Get(ctx context.Context, id int64) (Parent, error) {
	u, err := s.db.GetParent(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Parent{}, errNotFound
	}
	if err != nil {
		return Parent{}, err
	}
	return toParent(u), nil
}

// Invite is a pending invite link.
type Invite struct {
	ID            int64     `json:"id"`
	Role          string    `json:"role" enum:"parent,owner"`
	CreatedByName string    `json:"createdBy,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// CreateInvite issues an invite and returns it with its token (shown once, as a link and QR).
func (s *Service) CreateInvite(ctx context.Context, by *auth.Principal, role string) (Invite, string, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return Invite{}, "", err
	}
	now := s.clock.Now()
	row, err := s.db.CreateInvite(ctx, sqlc.CreateInviteParams{
		TokenHash: auth.HashToken(token), Role: role, CreatedBy: sql.NullInt64{Int64: by.UserID, Valid: true},
		CreatedAt: db.Timestamp(now), ExpiresAt: db.Timestamp(now.Add(InviteTTL)),
	})
	if err != nil {
		return Invite{}, "", fmt.Errorf("create invite: %w", err)
	}
	return Invite{ID: row.ID, Role: row.Role, CreatedAt: now, ExpiresAt: now.Add(InviteTTL)}, token, nil
}

func (s *Service) PendingInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.db.ListPendingInvites(ctx, db.Timestamp(s.clock.Now()))
	if err != nil {
		return nil, err
	}
	out := make([]Invite, 0, len(rows))
	for _, r := range rows {
		created, _ := db.ParseTimestamp(r.CreatedAt)
		expires, _ := db.ParseTimestamp(r.ExpiresAt)
		out = append(out, Invite{ID: r.ID, Role: r.Role, CreatedByName: r.CreatedByName.String, CreatedAt: created, ExpiresAt: expires})
	}
	return out, nil
}

func (s *Service) RevokeInvite(ctx context.Context, id int64) error {
	n, err := s.db.DeleteInvite(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "no such pending invite")
	}
	return nil
}

// InviteInfo is what the invite page shows before accepting.
type InviteInfo struct {
	ID            int64
	Role          string
	CreatedByName string
	ExpiresAt     time.Time
}

// LookupInvite checks an invite token without using it.
func (s *Service) LookupInvite(ctx context.Context, token string) (InviteInfo, error) {
	return lookup(ctx, s.db.Queries, token, s.clock.Now())
}

func lookup(ctx context.Context, q *sqlc.Queries, token string, now time.Time) (InviteInfo, error) {
	if token == "" {
		return InviteInfo{}, errInvalidInvite
	}
	row, err := q.GetInviteByHash(ctx, auth.HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return InviteInfo{}, errInvalidInvite
	}
	if err != nil {
		return InviteInfo{}, err
	}
	expires, err := db.ParseTimestamp(row.ExpiresAt)
	if err != nil || row.UsedAt.Valid || !now.Before(expires) {
		return InviteInfo{}, errInvalidInvite
	}
	return InviteInfo{ID: row.ID, Role: row.Role, CreatedByName: row.CreatedByName.String, ExpiresAt: expires}, nil
}

// NewParent is the account an invitee creates. Exactly one of PasswordHash and OIDCSubject is set.
type NewParent struct {
	DisplayName  string
	Username     string
	PasswordHash string
	OIDCSubject  string
}

// Accept uses an invite to create a parent, and returns the new parent with a web session token.
func (s *Service) Accept(ctx context.Context, token string, np NewParent) (int64, string, error) {
	np.DisplayName = strings.TrimSpace(np.DisplayName)
	np.Username = strings.ToLower(strings.TrimSpace(np.Username))
	var details []error
	if np.DisplayName == "" || len(np.DisplayName) > 40 {
		details = append(details, &huma.ErrorDetail{Location: "body.displayName", Message: "enter a name of up to 40 characters"})
	}
	if np.Username != "" && !UsernamePattern.MatchString(np.Username) {
		details = append(details, &huma.ErrorDetail{Location: "body.username", Message: "3-32 lowercase letters, digits, dot, underscore or hyphen"})
	}
	if np.Username == "" && np.OIDCSubject == "" {
		details = append(details, &huma.ErrorDetail{Location: "body.username", Message: "a username is required"})
	}
	if len(details) > 0 {
		// Don't reveal anything about the household to someone without a valid invite.
		if _, err := s.LookupInvite(ctx, token); err != nil {
			return 0, "", err
		}
		return 0, "", apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}

	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.db.WithTx(tx)
	now := s.clock.Now()
	inv, err := lookup(ctx, q, token, now)
	if err != nil {
		return 0, "", err
	}
	if np.Username != "" {
		if taken, err := q.UsernameTaken(ctx, sql.NullString{String: np.Username, Valid: true}); err != nil {
			return 0, "", err
		} else if taken {
			return 0, "", errTaken
		}
	}
	if np.OIDCSubject != "" {
		if _, err := q.GetParentByOIDCSubject(ctx, sql.NullString{String: np.OIDCSubject, Valid: true}); err == nil {
			return 0, "", apierr.New(http.StatusConflict, apierr.CodeConflict, "that identity provider account already belongs to a parent")
		}
	}
	var owner int64
	if inv.Role == "owner" {
		owner = 1
	}
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		DisplayName: np.DisplayName, Role: "parent", IsOwner: owner,
		Username:     sql.NullString{String: np.Username, Valid: np.Username != ""},
		PasswordHash: sql.NullString{String: np.PasswordHash, Valid: np.PasswordHash != ""},
	})
	if err != nil {
		return 0, "", fmt.Errorf("create parent: %w", err)
	}
	if np.OIDCSubject != "" {
		if err := q.SetOIDCSubject(ctx, sqlc.SetOIDCSubjectParams{OidcSubject: sql.NullString{String: np.OIDCSubject, Valid: true}, ID: u.ID}); err != nil {
			return 0, "", err
		}
	}
	if n, err := q.UseInvite(ctx, sqlc.UseInviteParams{
		UsedAt: sql.NullString{String: db.Timestamp(now), Valid: true}, UsedBy: sql.NullInt64{Int64: u.ID, Valid: true},
		ID: inv.ID, ExpiresAt: db.Timestamp(now),
	}); err != nil {
		return 0, "", err
	} else if n == 0 {
		return 0, "", errInvalidInvite
	}
	session, _, err := auth.NewStore(q, s.clock).Issue(ctx, auth.IssueParams{
		Kind: auth.TokenWebSession, UserID: u.ID, TTL: auth.WebSessionTTL, Label: "Invite",
	})
	if err != nil {
		return 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	return u.ID, session, nil
}

// SetOwner promotes or demotes a parent, keeping at least one owner.
func (s *Service) SetOwner(ctx context.Context, id int64, owner bool) error {
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		u, err := q.GetParent(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if !owner && u.IsOwner == 1 {
			if err := keepAnOwner(ctx, q); err != nil {
				return err
			}
		}
		var v int64
		if owner {
			v = 1
		}
		return q.SetOwner(ctx, sqlc.SetOwnerParams{IsOwner: v, ID: id})
	})
}

func keepAnOwner(ctx context.Context, q *sqlc.Queries) error {
	n, err := q.CountOwners(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errLastOwner
	}
	return nil
}

// Remove removes a parent: their sessions, app tokens, credentials and username go, their row
// (and name on past entries) stays. The last owner can't be removed.
func (s *Service) Remove(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		u, err := q.GetParent(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if u.IsOwner == 1 {
			if err := keepAnOwner(ctx, q); err != nil {
				return err
			}
		}
		if err := q.DeleteTokensForUser(ctx, sql.NullInt64{Int64: id, Valid: true}); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, id); err != nil {
			return err
		}
		return q.RemoveParent(ctx, sqlc.RemoveParentParams{RemovedAt: sql.NullString{String: db.Timestamp(s.clock.Now()), Valid: true}, ID: id})
	})
}

// ResetPassword gives a parent a new random password (returned once, to pass on) and signs them
// out everywhere. 2FA stays as it was.
func (s *Service) ResetPassword(ctx context.Context, id int64) (string, error) {
	codes, err := auth.NewRecoveryCodes(1) // same alphabet: easy to read out or type
	if err != nil {
		return "", err
	}
	password := codes[0]
	hash, err := auth.HashSecret(password)
	if err != nil {
		return "", err
	}
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetParent(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if err := q.SetPasswordHash(ctx, sqlc.SetPasswordHashParams{PasswordHash: sql.NullString{String: hash, Valid: true}, ID: id}); err != nil {
			return err
		}
		return q.DeleteTokensForUser(ctx, sql.NullInt64{Int64: id, Valid: true})
	})
	return password, err
}

// DisableTOTP turns off a parent's two-factor sign-in and deletes their recovery codes.
func (s *Service) DisableTOTP(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetParent(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if err := q.DisableTOTP(ctx, id); err != nil {
			return err
		}
		return q.DeleteRecoveryCodes(ctx, id)
	})
}

// ByUsername finds a parent for the CLI.
func (s *Service) ByUsername(ctx context.Context, username string) (sqlc.User, error) {
	u, err := s.db.GetParentByUsername(ctx, sql.NullString{String: strings.ToLower(strings.TrimSpace(username)), Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.User{}, fmt.Errorf("no parent with username %q", username)
	}
	return u, err
}

// PurgeExpiredInvites deletes unused invites past their expiry (daily maintenance).
func (s *Service) PurgeExpiredInvites(ctx context.Context) (int64, error) {
	return s.db.DeleteExpiredInvites(ctx, db.Timestamp(s.clock.Now()))
}

func (s *Service) inTx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(s.db.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
