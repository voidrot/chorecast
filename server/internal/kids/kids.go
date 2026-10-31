package kids

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/household"
)

var (
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	pinPattern   = regexp.MustCompile(`^[0-9]{4,8}$`)
	weekdays     = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
)

// DailyWindow is a daily time window (HH:MM–HH:MM). End before start wraps past midnight.
type DailyWindow struct {
	Start string `json:"start" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" example:"20:00"`
	End   string `json:"end" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" example:"07:00"`
}

// SchoolHours is a weekday window used as extra quiet time for the kid's notifications.
type SchoolHours struct {
	Days  []string `json:"days" nullable:"false" uniqueItems:"true" doc:"Lowercase weekday names, e.g. monday"`
	Start string   `json:"start" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" example:"08:00"`
	End   string   `json:"end" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" example:"15:00"`
}

// Kid is a kid profile as the API returns it.
type Kid struct {
	ID             int64        `json:"id"`
	Slug           string       `json:"slug" doc:"Stable short name usable anywhere a kid id is"`
	DisplayName    string       `json:"displayName"`
	Color          string       `json:"color"`
	Avatar         string       `json:"avatar,omitempty" doc:"Built-in avatar id; the image is /avatars/<id>.svg"`
	BirthDate      caltime.Date `json:"birthDate,omitempty" format:"date"`
	PINRequired    bool         `json:"pinRequired"`
	PINSet         bool         `json:"pinSet"`
	DayEndOverride string       `json:"dayEndOverride,omitempty" doc:"HH:MM; the kid's day ends earlier than the household's"`
	QuietHours     *DailyWindow `json:"quietHours,omitempty"`
	SchoolHours    *SchoolHours `json:"schoolHours,omitempty"`
	ArchivedAt     *time.Time   `json:"archivedAt,omitempty"`
}

// KidInput is a kid profile as parents create or edit it. Optional fields left empty are cleared.
type KidInput struct {
	DisplayName    string       `json:"displayName" minLength:"1" maxLength:"40" doc:"Nickname or first name only"`
	Slug           string       `json:"slug,omitempty" maxLength:"32" doc:"Defaults to one derived from the name on create; unchanged on update when empty"`
	Color          string       `json:"color" pattern:"^#[0-9a-fA-F]{6}$"`
	Avatar         string       `json:"avatar,omitempty"`
	BirthDate      caltime.Date `json:"birthDate,omitempty" format:"date"`
	PINRequired    bool         `json:"pinRequired,omitempty"`
	DayEndOverride string       `json:"dayEndOverride,omitempty" pattern:"^(([01][0-9]|2[0-3]):[0-5][0-9])?$"`
	QuietHours     *DailyWindow `json:"quietHours,omitempty"`
	SchoolHours    *SchoolHours `json:"schoolHours,omitempty"`
}

// Service manages kid profiles.
type Service struct {
	db    *db.DB
	clock clock.Clock
}

func NewService(d *db.DB, c clock.Clock) *Service { return &Service{db: d, clock: c} }

func toKid(u sqlc.User) Kid {
	k := Kid{
		ID: u.ID, Slug: u.Slug.String, DisplayName: u.DisplayName, Color: u.Color.String, Avatar: u.Avatar.String,
		PINRequired: u.PinRequired == 1, PINSet: u.PinHash.Valid, DayEndOverride: u.DayEndOverride.String,
	}
	if d, err := caltime.ParseDate(u.BirthDate.String); err == nil && u.BirthDate.Valid {
		k.BirthDate = d
	}
	if u.QuietStart.Valid && u.QuietEnd.Valid {
		k.QuietHours = &DailyWindow{Start: u.QuietStart.String, End: u.QuietEnd.String}
	}
	if u.SchoolDays != 0 && u.SchoolStart.Valid && u.SchoolEnd.Valid {
		sh := &SchoolHours{Days: []string{}, Start: u.SchoolStart.String, End: u.SchoolEnd.String}
		for i, name := range weekdays {
			if u.SchoolDays&(1<<i) != 0 {
				sh.Days = append(sh.Days, name)
			}
		}
		k.SchoolHours = sh
	}
	if u.ArchivedAt.Valid {
		if t, err := db.ParseTimestamp(u.ArchivedAt.String); err == nil {
			k.ArchivedAt = &t
		}
	}
	return k
}

// List returns kids by name; archived kids (last) only when includeArchived.
func (s *Service) List(ctx context.Context, includeArchived bool) ([]Kid, error) {
	rows, err := s.db.ListKids(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	out := make([]Kid, 0, len(rows))
	for _, u := range rows {
		out = append(out, toKid(u))
	}
	return out, nil
}

// Get resolves a kid by id or slug (archived kids included).
func (s *Service) Get(ctx context.Context, ref string) (Kid, error) {
	u, err := Resolve(ctx, s.db.Queries, ref)
	if err != nil {
		return Kid{}, err
	}
	return toKid(u), nil
}

// validated is an KidInput checked and converted to column values.
type validated struct {
	name, color, avatar, birth, dayEnd, quietStart, quietEnd, schoolStart, schoolEnd string
	schoolDays                                                                       int64
	pinRequired                                                                      int64
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func (s *Service) validate(ctx context.Context, in KidInput) (validated, error) {
	var details []error
	bad := func(loc, msg string, value any) {
		details = append(details, &huma.ErrorDetail{Location: "body." + loc, Message: msg, Value: value})
	}
	v := validated{name: strings.TrimSpace(in.DisplayName), color: strings.ToLower(in.Color), avatar: in.Avatar}
	if v.name == "" {
		bad("displayName", "a name is required", in.DisplayName)
	}
	if !colorPattern.MatchString(in.Color) {
		bad("color", "use a #rrggbb color", in.Color)
	}
	if in.Avatar != "" && !slices.Contains(AvatarIDs, in.Avatar) {
		bad("avatar", "unknown avatar", in.Avatar)
	}
	if !in.BirthDate.IsZero() {
		if in.BirthDate.After(caltime.DateOf(s.clock.Now())) {
			bad("birthDate", "birth date is in the future", in.BirthDate.String())
		}
		v.birth = in.BirthDate.String()
	}
	if in.PINRequired {
		v.pinRequired = 1
	}
	if in.DayEndOverride != "" {
		t, err := caltime.ParseTimeOfDay(in.DayEndOverride)
		if err != nil {
			bad("dayEndOverride", err.Error(), in.DayEndOverride)
		} else if hh, err := s.household(ctx); err != nil {
			return validated{}, err
		} else if _, err := hh.EffectiveDayEnd(&t); err != nil {
			bad("dayEndOverride", fmt.Sprintf("must be at or before the household day end (%s)", hh.DayEnd), in.DayEndOverride)
		}
		v.dayEnd = in.DayEndOverride
	}
	if q := in.QuietHours; q != nil {
		if !validWindow(q.Start, q.End, true) {
			bad("quietHours", "use HH:MM start and end times that differ", q)
		}
		v.quietStart, v.quietEnd = q.Start, q.End
	}
	if sh := in.SchoolHours; sh != nil && len(sh.Days) > 0 {
		for _, d := range sh.Days {
			i := slices.Index(weekdays, d)
			if i < 0 {
				bad("schoolHours.days", "unknown weekday", d)
				continue
			}
			v.schoolDays |= 1 << i
		}
		if !validWindow(sh.Start, sh.End, false) {
			bad("schoolHours", "school hours need a start before the end", sh)
		}
		v.schoolStart, v.schoolEnd = sh.Start, sh.End
	}
	if len(details) > 0 {
		return validated{}, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}
	return v, nil
}

// validWindow checks HH:MM times; wrapping past midnight is allowed only when wrap.
func validWindow(start, end string, wrap bool) bool {
	a, err1 := caltime.ParseTimeOfDay(start)
	b, err2 := caltime.ParseTimeOfDay(end)
	if err1 != nil || err2 != nil || a == b {
		return false
	}
	return wrap || a.Hour()*60+a.Minute() < b.Hour()*60+b.Minute()
}

func (s *Service) household(ctx context.Context) (caltime.Household, error) {
	return household.Calendar(ctx, s.db.Queries)
}

var errSlugTaken = apierr.New(http.StatusConflict, apierr.CodeConflict, "that short name is used by someone else")

func (s *Service) checkSlug(ctx context.Context, slug string, id int64) error {
	if err := ValidateSlug(slug); err != nil {
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, err.Error(),
			&huma.ErrorDetail{Location: "body.slug", Message: err.Error(), Value: slug})
	}
	taken, err := s.db.SlugTakenByOther(ctx, sqlc.SlugTakenByOtherParams{Slug: nullable(slug), ID: id})
	if err != nil {
		return err
	}
	if taken {
		return errSlugTaken
	}
	return nil
}

// Create adds a kid. Without a slug, one is derived from the name.
func (s *Service) Create(ctx context.Context, in KidInput) (Kid, error) {
	v, err := s.validate(ctx, in)
	if err != nil {
		return Kid{}, err
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		if slug, err = UniqueSlug(ctx, s.db.Queries, Slugify(v.name)); err != nil {
			return Kid{}, err
		}
	} else if err := s.checkSlug(ctx, slug, 0); err != nil {
		return Kid{}, err
	}
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return Kid{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.db.WithTx(tx)
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{DisplayName: v.name, Role: "child", Slug: nullable(slug)})
	if err != nil {
		return Kid{}, fmt.Errorf("create kid: %w", err)
	}
	if err := write(ctx, q, u.ID, slug, v); err != nil {
		return Kid{}, err
	}
	if err := tx.Commit(); err != nil {
		return Kid{}, err
	}
	return s.Get(ctx, slug)
}

// Update replaces a kid's profile. An empty slug keeps the current one.
func (s *Service) Update(ctx context.Context, ref string, in KidInput) (Kid, error) {
	kid, err := Resolve(ctx, s.db.Queries, ref)
	if err != nil {
		return Kid{}, err
	}
	v, err := s.validate(ctx, in)
	if err != nil {
		return Kid{}, err
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = kid.Slug.String
	} else if slug != kid.Slug.String {
		if err := s.checkSlug(ctx, slug, kid.ID); err != nil {
			return Kid{}, err
		}
	}
	if err := write(ctx, s.db.Queries, kid.ID, slug, v); err != nil {
		return Kid{}, err
	}
	return s.Get(ctx, slug)
}

func write(ctx context.Context, q *sqlc.Queries, id int64, slug string, v validated) error {
	return q.UpdateKid(ctx, sqlc.UpdateKidParams{
		DisplayName: v.name, Slug: nullable(slug), Color: nullable(v.color), Avatar: nullable(v.avatar),
		BirthDate: nullable(v.birth), PinRequired: v.pinRequired, DayEndOverride: nullable(v.dayEnd),
		QuietStart: nullable(v.quietStart), QuietEnd: nullable(v.quietEnd), SchoolDays: v.schoolDays,
		SchoolStart: nullable(v.schoolStart), SchoolEnd: nullable(v.schoolEnd), ID: id,
	})
}

// SetPIN sets (pin != "") or clears a kid's PIN.
func (s *Service) SetPIN(ctx context.Context, ref, pin string) error {
	kid, err := Resolve(ctx, s.db.Queries, ref)
	if err != nil {
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
	return s.db.SetKidPIN(ctx, sqlc.SetKidPINParams{PinHash: hash, ID: kid.ID})
}

// Archive hides a kid and revokes their devices; history is kept. Scheduling skips archived kids
// once it exists (#21-#24).
func (s *Service) Archive(ctx context.Context, ref string) (Kid, error) {
	kid, err := Resolve(ctx, s.db.Queries, ref)
	if err != nil {
		return Kid{}, err
	}
	if !kid.ArchivedAt.Valid {
		tx, err := s.db.SQL.BeginTx(ctx, nil)
		if err != nil {
			return Kid{}, err
		}
		defer func() { _ = tx.Rollback() }()
		q := s.db.WithTx(tx)
		if err := q.SetKidArchived(ctx, sqlc.SetKidArchivedParams{
			ArchivedAt: sql.NullString{String: db.Timestamp(s.clock.Now()), Valid: true}, ID: kid.ID,
		}); err != nil {
			return Kid{}, err
		}
		if err := q.DeleteTokensForUser(ctx, sql.NullInt64{Int64: kid.ID, Valid: true}); err != nil {
			return Kid{}, err
		}
		if err := tx.Commit(); err != nil {
			return Kid{}, err
		}
	}
	return s.Get(ctx, kid.Slug.String)
}

// Unarchive restores a kid. Devices must be paired again.
func (s *Service) Unarchive(ctx context.Context, ref string) (Kid, error) {
	kid, err := Resolve(ctx, s.db.Queries, ref)
	if err != nil {
		return Kid{}, err
	}
	if err := s.db.SetKidArchived(ctx, sqlc.SetKidArchivedParams{ID: kid.ID}); err != nil {
		return Kid{}, err
	}
	return s.Get(ctx, kid.Slug.String)
}

// DefaultAvatar picks a built-in avatar for the n-th kid created in one go (first-run setup).
func DefaultAvatar(n int) string { return AvatarIDs[n%len(AvatarIDs)] }
