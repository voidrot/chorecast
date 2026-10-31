// Package setup implements first run. Until a household exists, the server holds a single-use
// setup token (stored hashed, replaced on every start and printed in the log) and refuses every
// API call except health, version and setup with 503 setup_required. Completing setup creates
// the household, the first owner and any kids in one transaction, deletes the token and signs
// the owner in. See docs/features/household.md (First run).
package setup

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/kids"
)

// Weekdays are the API names of time.Weekday values, Sunday first.
var Weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// Service tracks whether setup is required and completes it.
type Service struct {
	db       *db.DB
	clock    clock.Clock
	required atomic.Bool
}

// New loads whether setup is still required.
func New(ctx context.Context, d *db.DB, c clock.Clock) (*Service, error) {
	exists, err := d.HouseholdExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("check household: %w", err)
	}
	s := &Service{db: d, clock: c}
	s.required.Store(!exists)
	return s, nil
}

// Required reports whether first-run setup is still pending. Cheap: checked on every request.
func (s *Service) Required() bool { return s.required.Load() }

// Begin issues a fresh setup token if setup is required, replacing any earlier one, and returns
// it for the caller to print. It returns "" once setup is done.
func (s *Service) Begin(ctx context.Context) (string, error) {
	if !s.Required() {
		return "", nil
	}
	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	if err := s.db.SetSetupToken(ctx, sqlc.SetSetupTokenParams{TokenHash: auth.HashToken(token), CreatedAt: db.Timestamp(s.clock.Now())}); err != nil {
		return "", fmt.Errorf("store setup token: %w", err)
	}
	return token, nil
}

// SetupURL is the link printed in the log: the web wizard with the token in the query.
func SetupURL(base, token string) string {
	return strings.TrimRight(base, "/") + "/setup?token=" + token
}

var (
	errComplete     = apierr.New(http.StatusConflict, apierr.CodeSetupComplete, "setup has already been completed")
	errInvalidToken = apierr.New(http.StatusForbidden, apierr.CodeInvalidSetupToken,
		"the setup token is invalid; open the newest setup link from the server log (it changes on every restart)")
)

// Verify checks a setup token without using it, so the wizard can reject a stale link before the
// user fills in any forms.
func (s *Service) Verify(ctx context.Context, token string) error {
	if !s.Required() {
		return errComplete
	}
	return checkToken(ctx, s.db.Queries, token)
}

type tokenQuerier interface {
	GetSetupTokenHash(ctx context.Context) ([]byte, error)
}

func checkToken(ctx context.Context, q tokenQuerier, token string) error {
	stored, err := q.GetSetupTokenHash(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return errInvalidToken
	}
	if err != nil {
		return fmt.Errorf("read setup token: %w", err)
	}
	if token == "" || subtle.ConstantTimeCompare(stored, auth.HashToken(token)) != 1 {
		return errInvalidToken
	}
	return nil
}

// SetupHousehold is the household step of the wizard.
type SetupHousehold struct {
	Name      string `json:"name" minLength:"1" maxLength:"60" example:"The Bradys"`
	Timezone  string `json:"timezone" minLength:"1" maxLength:"64" example:"America/Chicago" doc:"IANA timezone"`
	DayEnd    string `json:"dayEnd" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" default:"00:00" example:"00:00" doc:"When the household's day ends (HH:MM); 00:00 means midnight"`
	WeekStart string `json:"weekStart" enum:"sunday,monday,tuesday,wednesday,thursday,friday,saturday" default:"monday"`
}

// SetupOwner is the first owner account.
type SetupOwner struct {
	DisplayName string `json:"displayName" minLength:"1" maxLength:"40" example:"Mom"`
	Username    string `json:"username" pattern:"^[a-z0-9][a-z0-9._-]{2,31}$" example:"mom" doc:"3-32 lowercase letters, digits, dot, underscore or hyphen"`
	Password    string `json:"password" minLength:"8" maxLength:"128"`
}

// SetupKid is a kid profile created during setup.
type SetupKid struct {
	DisplayName string       `json:"displayName" minLength:"1" maxLength:"40" example:"Sam" doc:"Nickname or first name"`
	BirthDate   caltime.Date `json:"birthDate,omitempty" format:"date" doc:"YYYY-MM-DD; used for age-based chore suggestions"`
	Color       string       `json:"color" pattern:"^#[0-9a-fA-F]{6}$" example:"#3b82f6"`
	PINRequired bool         `json:"pinRequired,omitempty" doc:"Require a PIN to open this kid's profile on shared devices"`
}

// SetupRequest is everything the wizard collects.
type SetupRequest struct {
	Token     string         `json:"token" minLength:"1" doc:"Setup token from the setup link in the server log"`
	Household SetupHousehold `json:"household"`
	Owner     SetupOwner     `json:"owner"`
	Kids      []SetupKid     `json:"kids,omitempty" maxItems:"20"`
}

// Result is what completing setup created.
type Result struct {
	OwnerID      int64
	SessionToken string // web session for the new owner
}

// Complete creates the household, owner and kids in one transaction, deletes the setup token and
// issues the owner a web session.
func (s *Service) Complete(ctx context.Context, req SetupRequest) (Result, error) {
	if !s.Required() {
		return Result{}, errComplete
	}
	if err := checkToken(ctx, s.db.Queries, req.Token); err != nil {
		return Result{}, err
	}
	cal, err := validate(req, caltime.DateOf(s.clock.Now()))
	if err != nil {
		return Result{}, err
	}
	// Hash before the transaction: argon2 is slow and the database has one connection.
	pwHash, err := auth.HashSecret(req.Owner.Password)
	if err != nil {
		return Result{}, err
	}

	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.db.WithTx(tx)

	// Re-check inside the transaction: a concurrent request may have completed setup first.
	if exists, err := q.HouseholdExists(ctx); err != nil {
		return Result{}, err
	} else if exists {
		return Result{}, errComplete
	}
	if err := checkToken(ctx, q, req.Token); err != nil {
		return Result{}, err
	}

	now := db.Timestamp(s.clock.Now())
	if err := q.CreateHousehold(ctx, sqlc.CreateHouseholdParams{
		Name: strings.TrimSpace(req.Household.Name), Timezone: req.Household.Timezone,
		DayEnd: cal.DayEnd.String(), WeekStart: int64(cal.WeekStart), CreatedAt: now,
	}); err != nil {
		return Result{}, fmt.Errorf("create household: %w", err)
	}
	owner, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		DisplayName:  strings.TrimSpace(req.Owner.DisplayName),
		Role:         "parent",
		IsOwner:      1,
		Username:     sql.NullString{String: req.Owner.Username, Valid: true},
		PasswordHash: sql.NullString{String: pwHash, Valid: true},
	})
	if err != nil {
		return Result{}, fmt.Errorf("create owner: %w", err)
	}
	for i, k := range req.Kids {
		slug, err := kids.UniqueSlug(ctx, q, kids.Slugify(k.DisplayName))
		if err != nil {
			return Result{}, fmt.Errorf("kid %d slug: %w", i, err)
		}
		var pinRequired int64
		if k.PINRequired {
			pinRequired = 1
		}
		if _, err := q.CreateUser(ctx, sqlc.CreateUserParams{
			DisplayName: strings.TrimSpace(k.DisplayName),
			Role:        "child",
			Slug:        sql.NullString{String: slug, Valid: true},
			PinRequired: pinRequired,
			Color:       sql.NullString{String: strings.ToLower(k.Color), Valid: true},
			BirthDate:   sql.NullString{String: k.BirthDate.String(), Valid: !k.BirthDate.IsZero()},
		}); err != nil {
			return Result{}, fmt.Errorf("create kid %d: %w", i, err)
		}
	}
	if err := q.DeleteSetupToken(ctx); err != nil {
		return Result{}, fmt.Errorf("delete setup token: %w", err)
	}
	session, _, err := auth.NewStore(q, s.clock).Issue(ctx, auth.IssueParams{
		Kind: auth.TokenWebSession, UserID: owner.ID, Label: "setup", TTL: auth.WebSessionTTL,
	})
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	s.required.Store(false)
	return Result{OwnerID: owner.ID, SessionToken: session}, nil
}

// validate checks what the schema can't express and returns the household calendar.
func validate(req SetupRequest, today caltime.Date) (caltime.Household, error) {
	var details []error
	invalid := func(loc, msg string, value any) {
		details = append(details, &huma.ErrorDetail{Location: loc, Message: msg, Value: value})
	}

	h := req.Household
	if strings.TrimSpace(h.Name) == "" {
		invalid("body.household.name", "name is required", h.Name)
	}
	dayEnd, err := caltime.ParseTimeOfDay(h.DayEnd)
	if err != nil {
		invalid("body.household.dayEnd", err.Error(), h.DayEnd)
	}
	weekStart := time.Monday
	if h.WeekStart != "" {
		weekStart = -1
		for i, name := range Weekdays {
			if name == h.WeekStart {
				weekStart = time.Weekday(i)
			}
		}
	}
	var cal caltime.Household
	if h.Timezone == "Local" {
		invalid("body.household.timezone", "use an IANA timezone name such as Europe/Berlin", h.Timezone)
	} else if cal, err = caltime.NewHousehold(h.Timezone, dayEnd, weekStart); err != nil {
		invalid("body.household.timezone", err.Error(), h.Timezone)
	}
	if strings.TrimSpace(req.Owner.DisplayName) == "" {
		invalid("body.owner.displayName", "display name is required", req.Owner.DisplayName)
	}
	for i, k := range req.Kids {
		if strings.TrimSpace(k.DisplayName) == "" {
			invalid(fmt.Sprintf("body.kids[%d].displayName", i), "name is required", k.DisplayName)
		}
		if !k.BirthDate.IsZero() && k.BirthDate.After(today) {
			invalid(fmt.Sprintf("body.kids[%d].birthDate", i), "birth date is in the future", k.BirthDate.String())
		}
	}
	if len(details) > 0 {
		return caltime.Household{}, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed,
			"validation failed", details...)
	}
	return cal, nil
}

const metaDuringSetup = "chorecast:setup"

// AllowDuringSetup marks operation metadata as reachable while setup is pending (health,
// version, setup itself). Everything else answers 503 setup_required until setup completes.
func AllowDuringSetup(meta map[string]any) map[string]any {
	out := map[string]any{metaDuringSetup: true}
	for k, v := range meta {
		out[k] = v
	}
	return out
}

// Gate is Huma middleware refusing operations not allowed during setup while it is pending.
// A nil service (spec generation) disables it.
func Gate(api huma.API, s *Service) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if s != nil && s.Required() {
			if allowed, _ := ctx.Operation().Metadata[metaDuringSetup].(bool); !allowed {
				_ = huma.WriteErr(api, ctx, http.StatusServiceUnavailable,
					"first-run setup has not been completed", apierr.WithCode(apierr.CodeSetupRequired))
				return
			}
		}
		next(ctx)
	}
}
