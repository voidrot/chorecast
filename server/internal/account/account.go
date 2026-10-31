// Package account implements parent sign-in and self-service account management: password
// login with optional TOTP (or a single-use recovery code), web sessions and app tokens,
// session listing and revocation, password change and the parent PIN. Failed logins are rate
// limited per username+IP, per IP and per username.
package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// Login rate limits. The pair limit stops guessing one account from one place; the IP limit
// stops spraying many accounts; the account limit slows distributed guessing while keeping a
// lockout by strangers short-lived.
const (
	pairLimit, pairWindow       = 5, 15 * time.Minute
	ipLimit, ipWindow           = 20, 15 * time.Minute
	accountLimit, accountWindow = 50, time.Hour

	MinPasswordLength = 8
	totpIssuer        = "Chorecast"
)

var pinPattern = regexp.MustCompile(`^[0-9]{4,8}$`)

// Client is who a login is for: the web admin (cookie session) or the Android app (bearer token).
type Client string

const (
	ClientWeb Client = "web"
	ClientApp Client = "app"
)

// Service implements sign-in and account management.
type Service struct {
	db      *db.DB
	tokens  *auth.Store
	clock   clock.Clock
	pair    *auth.Limiter
	ip      *auth.Limiter
	account *auth.Limiter
}

func New(d *db.DB, c clock.Clock) *Service {
	var q *sqlc.Queries
	if d != nil {
		q = d.Queries
	}
	return &Service{
		db:      d,
		tokens:  auth.NewStore(q, c),
		clock:   c,
		pair:    auth.NewLimiter(c, pairLimit, pairWindow),
		ip:      auth.NewLimiter(c, ipLimit, ipWindow),
		account: auth.NewLimiter(c, accountLimit, accountWindow),
	}
}

// LoginInput is a sign-in attempt.
type LoginInput struct {
	Username     string
	Password     string
	TOTPCode     string
	RecoveryCode string
	Client       Client
	Label        string // app: device name; web: derived from the user agent when empty
}

// LoginResult is a successful sign-in.
type LoginResult struct {
	UserID int64
	Token  string
	// RecoveryCodesLeft is set when a recovery code was used, so clients can warn when few remain.
	RecoveryCodesLeft *int64
}

var (
	errInvalidCredentials = apierr.New(http.StatusUnauthorized, apierr.CodeInvalidCredentials, "wrong username or password")
	errTOTPRequired       = apierr.New(http.StatusUnauthorized, apierr.CodeTOTPRequired,
		"enter the code from your authenticator app, or a recovery code")
	errInvalidTOTP     = apierr.New(http.StatusUnauthorized, apierr.CodeInvalidTOTP, "that code is wrong or was already used")
	errInvalidPassword = apierr.New(http.StatusForbidden, apierr.CodeInvalidPassword, "your password is wrong")
)

// dummyHash is verified against for unknown usernames, so a miss costs as much time as a hit.
var dummyHash = sync.OnceValue(func() string {
	h, _ := auth.HashSecret("chorecast-dummy-password")
	return h
})

// Login verifies credentials and issues a web session or app token.
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	username := strings.ToLower(strings.TrimSpace(in.Username))
	ip := auth.RequestInfoFrom(ctx).IP
	pairKey := username + "|" + ip
	if err := s.checkLimits(pairKey, ip, username); err != nil {
		return LoginResult{}, err
	}
	fail := func(err error) (LoginResult, error) {
		s.pair.Fail(pairKey)
		s.ip.Fail(ip)
		s.account.Fail(username)
		return LoginResult{}, err
	}

	user, err := s.db.GetParentByUsername(ctx, sql.NullString{String: username, Valid: username != ""})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !user.PasswordHash.Valid) {
		_, _, _ = auth.VerifySecret(in.Password, dummyHash())
		return fail(errInvalidCredentials)
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("find user: %w", err)
	}
	ok, rehash, err := auth.VerifySecret(in.Password, user.PasswordHash.String)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return fail(errInvalidCredentials)
	}

	var res LoginResult
	if user.TotpEnabled == 1 {
		switch {
		case in.TOTPCode != "":
			step, ok := auth.VerifyTOTP(user.TotpSecret.String, in.TOTPCode, s.clock.Now(), user.TotpLastStep)
			if !ok {
				return fail(errInvalidTOTP)
			}
			if err := s.db.SetTOTPLastStep(ctx, sqlc.SetTOTPLastStepParams{TotpLastStep: step, ID: user.ID}); err != nil {
				return LoginResult{}, err
			}
		case in.RecoveryCode != "":
			left, err := s.useRecoveryCode(ctx, user.ID, in.RecoveryCode)
			if err != nil {
				return LoginResult{}, err
			}
			if left < 0 {
				return fail(errInvalidTOTP)
			}
			res.RecoveryCodesLeft = &left
		default:
			// The password was right; don't count this as a failure.
			return LoginResult{}, errTOTPRequired
		}
	}

	if rehash {
		if h, err := auth.HashSecret(in.Password); err == nil {
			_ = s.db.SetPasswordHash(ctx, sqlc.SetPasswordHashParams{PasswordHash: sql.NullString{String: h, Valid: true}, ID: user.ID})
		}
	}
	params := auth.IssueParams{Kind: auth.TokenApp, UserID: user.ID, Label: label(in.Label, "Android app")}
	if in.Client != ClientApp {
		params = auth.IssueParams{Kind: auth.TokenWebSession, UserID: user.ID, TTL: auth.WebSessionTTL,
			Label: label(in.Label, browserLabel(auth.RequestInfoFrom(ctx).UserAgent))}
	}
	token, _, err := s.tokens.Issue(ctx, params)
	if err != nil {
		return LoginResult{}, err
	}
	s.pair.Reset(pairKey)
	res.UserID, res.Token = user.ID, token
	return res, nil
}

func (s *Service) checkLimits(pairKey, ip, username string) error {
	for _, c := range []struct {
		l   *auth.Limiter
		key string
	}{{s.pair, pairKey}, {s.ip, ip}, {s.account, username}} {
		if blocked, wait := c.l.Blocked(c.key); blocked {
			secs := max(int(wait.Round(time.Second)/time.Second), 1)
			return huma.ErrorWithHeaders(
				apierr.New(http.StatusTooManyRequests, apierr.CodeRateLimited,
					fmt.Sprintf("too many failed sign-ins; try again in %d minutes", (secs+59)/60)),
				http.Header{"Retry-After": {strconv.Itoa(secs)}})
		}
	}
	return nil
}

// useRecoveryCode marks a recovery code used and returns how many remain, or -1 if it was invalid.
func (s *Service) useRecoveryCode(ctx context.Context, userID int64, code string) (int64, error) {
	n, err := s.db.UseRecoveryCode(ctx, sqlc.UseRecoveryCodeParams{
		UsedAt: sql.NullString{String: db.Timestamp(s.clock.Now()), Valid: true}, UserID: userID, CodeHash: auth.HashRecoveryCode(code),
	})
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return -1, nil
	}
	return s.db.CountUnusedRecoveryCodes(ctx, userID)
}

func label(given, fallback string) string {
	given = strings.TrimSpace(given)
	if given == "" {
		given = fallback
	}
	if len(given) > 100 {
		given = given[:100]
	}
	return given
}

// browserLabel turns a user agent into a short "Firefox on Linux" style label.
func browserLabel(ua string) string {
	browser, os := "Browser", ""
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	for _, o := range []struct{ token, name string }{
		{"Android", "Android"}, {"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Windows", "Windows"},
		{"Mac OS X", "macOS"}, {"CrOS", "ChromeOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.token) {
			os = o.name
			break
		}
	}
	if os == "" {
		return browser
	}
	return browser + " on " + os
}

// Logout revokes the caller's own token.
func (s *Service) Logout(ctx context.Context, tokenID int64) error {
	return s.tokens.Revoke(ctx, tokenID)
}

// Session is one of a parent's web sessions or app tokens.
type Session struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind" enum:"web_session,app"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	Current    bool       `json:"current" doc:"The session making this request"`
}

// Sessions lists a parent's live sessions, most recently used first.
func (s *Service) Sessions(ctx context.Context, p *auth.Principal) ([]Session, error) {
	rows, err := s.db.ListSessionsForUser(ctx, sqlc.ListSessionsForUserParams{
		UserID: sql.NullInt64{Int64: p.UserID, Valid: true}, ExpiresAt: sql.NullString{String: db.Timestamp(s.clock.Now()), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		created, _ := db.ParseTimestamp(r.CreatedAt)
		out = append(out, Session{
			ID: r.ID, Kind: r.Kind, Label: r.Label, CreatedAt: created,
			LastUsedAt: optTime(r.LastUsedAt), ExpiresAt: optTime(r.ExpiresAt), Current: r.ID == p.TokenID,
		})
	}
	return out, nil
}

func optTime(v sql.NullString) *time.Time {
	if !v.Valid {
		return nil
	}
	t, err := db.ParseTimestamp(v.String)
	if err != nil {
		return nil
	}
	return &t
}

// RevokeSession revokes one of the parent's own sessions.
func (s *Service) RevokeSession(ctx context.Context, p *auth.Principal, id int64) error {
	if _, err := s.db.GetTokenForUser(ctx, sqlc.GetTokenForUserParams{ID: id, UserID: sql.NullInt64{Int64: p.UserID, Valid: true}}); errors.Is(err, sql.ErrNoRows) {
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "no such session")
	} else if err != nil {
		return err
	}
	return s.tokens.Revoke(ctx, id)
}

// confirm loads the caller's row and checks their re-entered password.
func (s *Service) confirm(ctx context.Context, p *auth.Principal, password string) (sqlc.User, error) {
	u, err := s.db.GetUser(ctx, p.UserID)
	if err != nil {
		return sqlc.User{}, err
	}
	ok, _, err := auth.VerifySecret(password, u.PasswordHash.String)
	if err != nil && u.PasswordHash.Valid {
		return sqlc.User{}, err
	}
	if !ok {
		return sqlc.User{}, errInvalidPassword
	}
	return u, nil
}

// ChangePassword sets a new password and signs out the parent's other sessions.
func (s *Service) ChangePassword(ctx context.Context, p *auth.Principal, current, next string) error {
	if _, err := s.confirm(ctx, p, current); err != nil {
		return err
	}
	h, err := auth.HashSecret(next)
	if err != nil {
		return err
	}
	if err := s.db.SetPasswordHash(ctx, sqlc.SetPasswordHashParams{PasswordHash: sql.NullString{String: h, Valid: true}, ID: p.UserID}); err != nil {
		return err
	}
	return s.db.DeleteOtherSessionsForUser(ctx, sqlc.DeleteOtherSessionsForUserParams{
		UserID: sql.NullInt64{Int64: p.UserID, Valid: true}, ID: p.TokenID,
	})
}

// SetPIN sets (pin != "") or clears the parent PIN used for shared-tablet elevation.
func (s *Service) SetPIN(ctx context.Context, p *auth.Principal, password, pin string) error {
	if _, err := s.confirm(ctx, p, password); err != nil {
		return err
	}
	var hash sql.NullString
	if pin != "" {
		if !pinPattern.MatchString(pin) {
			return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "a PIN is 4-8 digits",
				&huma.ErrorDetail{Location: "body.pin", Message: "a PIN is 4-8 digits"})
		}
		h, err := auth.HashSecret(pin)
		if err != nil {
			return err
		}
		hash = sql.NullString{String: h, Valid: true}
	}
	return s.db.SetParentPINHash(ctx, sqlc.SetParentPINHashParams{ParentPinHash: hash, ID: p.UserID})
}

// BeginTOTP starts enrolment: it stores a new pending secret and returns it with the otpauth URL.
func (s *Service) BeginTOTP(ctx context.Context, p *auth.Principal) (secret, url string, err error) {
	u, err := s.db.GetUser(ctx, p.UserID)
	if err != nil {
		return "", "", err
	}
	if u.TotpEnabled == 1 {
		return "", "", apierr.New(http.StatusConflict, apierr.CodeTOTPAlreadyEnabled, "two-factor sign-in is already on")
	}
	issuer := totpIssuer
	if hh, err := s.db.GetHousehold(ctx); err == nil {
		issuer = totpIssuer + " (" + hh.Name + ")"
	}
	secret, url, err = auth.NewTOTPKey(issuer, u.Username.String)
	if err != nil {
		return "", "", err
	}
	err = s.db.SetPendingTOTPSecret(ctx, sqlc.SetPendingTOTPSecretParams{TotpSecret: sql.NullString{String: secret, Valid: true}, ID: p.UserID})
	return secret, url, err
}

// EnableTOTP finishes enrolment with a code from the authenticator app and returns fresh
// recovery codes (shown once).
func (s *Service) EnableTOTP(ctx context.Context, p *auth.Principal, code string) ([]string, error) {
	u, err := s.db.GetUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	switch {
	case u.TotpEnabled == 1:
		return nil, apierr.New(http.StatusConflict, apierr.CodeTOTPAlreadyEnabled, "two-factor sign-in is already on")
	case !u.TotpSecret.Valid:
		return nil, apierr.New(http.StatusConflict, apierr.CodeTOTPSetupNotStarted, "start two-factor setup first")
	}
	step, ok := auth.VerifyTOTP(u.TotpSecret.String, code, s.clock.Now(), 0)
	if !ok {
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidTOTP, "that code is wrong; check your device's clock")
	}
	return s.replaceRecoveryCodes(ctx, p.UserID, func(q *sqlc.Queries) error {
		return q.EnableTOTP(ctx, sqlc.EnableTOTPParams{TotpLastStep: step, ID: p.UserID})
	})
}

// DisableTOTP turns two-factor sign-in off and deletes the recovery codes.
func (s *Service) DisableTOTP(ctx context.Context, p *auth.Principal, password string) error {
	u, err := s.confirm(ctx, p, password)
	if err != nil {
		return err
	}
	if u.TotpEnabled == 0 {
		return apierr.New(http.StatusConflict, apierr.CodeTOTPNotEnabled, "two-factor sign-in is off")
	}
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		if err := q.DisableTOTP(ctx, p.UserID); err != nil {
			return err
		}
		return q.DeleteRecoveryCodes(ctx, p.UserID)
	})
}

// RegenerateRecoveryCodes replaces the recovery codes (shown once).
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, p *auth.Principal, password string) ([]string, error) {
	u, err := s.confirm(ctx, p, password)
	if err != nil {
		return nil, err
	}
	if u.TotpEnabled == 0 {
		return nil, apierr.New(http.StatusConflict, apierr.CodeTOTPNotEnabled, "two-factor sign-in is off")
	}
	return s.replaceRecoveryCodes(ctx, p.UserID, func(*sqlc.Queries) error { return nil })
}

func (s *Service) replaceRecoveryCodes(ctx context.Context, userID int64, also func(*sqlc.Queries) error) ([]string, error) {
	codes, err := auth.NewRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		if err := also(q); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
			return err
		}
		now := db.Timestamp(s.clock.Now())
		for _, c := range codes {
			if err := q.InsertRecoveryCode(ctx, sqlc.InsertRecoveryCodeParams{UserID: userID, CodeHash: auth.HashRecoveryCode(c), CreatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
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
