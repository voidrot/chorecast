// Package household holds the household's settings: name, calendar (timezone, day end, week start)
// and the policies other features read (negative balances, sibling visibility, media retention,
// auto-approve timeout, offline grace, streak milestones). Changing the calendar emits household.calendar_changed so
// the horizon generator can regenerate untouched instances. See docs/features/household.md.
package household

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
)

// Weekdays are the API names of time.Weekday values, Sunday first.
var Weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// HouseholdSettings are the household's settings as the API reads and writes them.
type HouseholdSettings struct {
	Name                 string `json:"name" minLength:"1" maxLength:"60"`
	Timezone             string `json:"timezone" minLength:"1" maxLength:"64" doc:"IANA timezone"`
	DayEnd               string `json:"dayEnd" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"HH:MM; 00:00 means midnight"`
	WeekStart            string `json:"weekStart" enum:"sunday,monday,tuesday,wednesday,thursday,friday,saturday"`
	AllowNegativeBalance bool   `json:"allowNegativeBalance" doc:"Deductions may take a kid below zero; otherwise they're clamped"`
	SiblingVisibility    string `json:"siblingVisibility" enum:"private,leaderboard" doc:"private: kids see only their own data; leaderboard: a weekly family ranking"`
	MediaRetentionDays   int    `json:"mediaRetentionDays" minimum:"1" maximum:"3650" doc:"Evidence photos and videos are deleted this long after a chore is resolved"`
	AutoApproveHours     *int   `json:"autoApproveHours,omitempty" minimum:"1" maximum:"720" doc:"Approve submitted chores after this many hours; absent = never"`
	OfflineGraceMinutes  int    `json:"offlineGraceMinutes" minimum:"0" maximum:"1440" doc:"Completions made offline count if done within this long after day close"`
	DefaultChoreMinutes  int    `json:"defaultChoreMinutes" minimum:"1" maximum:"240" doc:"Fair auto-pick counts a chore without an estimate as this many minutes"`
	// A new household starts with 3, 7 and 30 days. Left out of a PUT = cleared, like every
	// optional field.
	StreakMilestones []StreakMilestone `json:"streakMilestones,omitempty" maxItems:"20" doc:"Daily-streak lengths that earn a bonus, sorted by days; absent = no streak bonuses"`
}

// StreakMilestone is a daily-streak length that earns a streak_bonus entry when a streak reaches it.
type StreakMilestone struct {
	Days   int `json:"days" minimum:"1" maximum:"3650" doc:"Streak length in days; each milestone has its own"`
	Points int `json:"points" minimum:"1" maximum:"100000"`
}

// CalendarChanged is emitted when the timezone, day end or week start changes.
type CalendarChanged struct {
	Timezone  string `json:"timezone"`
	DayEnd    string `json:"dayEnd"`
	WeekStart string `json:"weekStart"`
}

func (CalendarChanged) EventType() string { return "household.calendar_changed" }

// Service reads and updates the settings. bus may be nil (no event is emitted then).
type Service struct {
	db  *db.DB
	bus *events.Bus
}

func New(d *db.DB, bus *events.Bus) *Service { return &Service{db: d, bus: bus} }

func toSettings(h sqlc.Household) (HouseholdSettings, error) {
	milestones, err := Milestones(h)
	if err != nil {
		return HouseholdSettings{}, err
	}
	s := HouseholdSettings{
		Name: h.Name, Timezone: h.Timezone, DayEnd: h.DayEnd, WeekStart: Weekdays[h.WeekStart%7],
		AllowNegativeBalance: h.AllowNegativeBalance == 1, SiblingVisibility: h.SiblingVisibility,
		MediaRetentionDays: int(h.MediaRetentionDays), OfflineGraceMinutes: int(h.OfflineGraceMinutes),
		DefaultChoreMinutes: int(h.DefaultChoreMinutes), StreakMilestones: milestones,
	}
	if h.AutoApproveHours.Valid {
		v := int(h.AutoApproveHours.Int64)
		s.AutoApproveHours = &v
	}
	return s, nil
}

// Milestones are the household's streak milestones, sorted by days.
func Milestones(h sqlc.Household) ([]StreakMilestone, error) {
	var out []StreakMilestone
	if err := json.Unmarshal([]byte(h.StreakMilestones), &out); err != nil {
		return nil, fmt.Errorf("household streak milestones: %w", err)
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context) (HouseholdSettings, error) {
	h, err := s.db.GetHousehold(ctx)
	if err != nil {
		return HouseholdSettings{}, fmt.Errorf("load household: %w", err)
	}
	return toSettings(h)
}

// Calendar returns the household's calendar for date arithmetic.
func Calendar(ctx context.Context, q *sqlc.Queries) (caltime.Household, error) {
	h, err := q.GetHousehold(ctx)
	if err != nil {
		return caltime.Household{}, fmt.Errorf("load household: %w", err)
	}
	end, err := caltime.ParseTimeOfDay(h.DayEnd)
	if err != nil {
		return caltime.Household{}, err
	}
	return caltime.NewHousehold(h.Timezone, end, time.Weekday(h.WeekStart%7))
}

// Update replaces the settings. Changing the day end is refused while a kid's day-end override
// would end after it; changing the calendar emits CalendarChanged in the same transaction.
func (s *Service) Update(ctx context.Context, in HouseholdSettings) (HouseholdSettings, error) {
	in.Name = strings.TrimSpace(in.Name)
	var details []error
	bad := func(loc, msg string, v any) {
		details = append(details, &huma.ErrorDetail{Location: "body." + loc, Message: msg, Value: v})
	}
	if in.Name == "" {
		bad("name", "a name is required", in.Name)
	}
	dayEnd, err := caltime.ParseTimeOfDay(in.DayEnd)
	if err != nil {
		bad("dayEnd", err.Error(), in.DayEnd)
	}
	weekStart := slices.Index(Weekdays, in.WeekStart)
	slices.SortStableFunc(in.StreakMilestones, func(a, b StreakMilestone) int { return a.Days - b.Days })
	for i := 1; i < len(in.StreakMilestones); i++ {
		if in.StreakMilestones[i].Days == in.StreakMilestones[i-1].Days {
			bad(fmt.Sprintf("streakMilestones[%d].days", i), "each milestone needs its own number of days", in.StreakMilestones[i].Days)
		}
	}
	if in.Timezone == "Local" {
		bad("timezone", "use an IANA timezone name such as Europe/Berlin", in.Timezone)
	} else if _, err := time.LoadLocation(in.Timezone); err != nil {
		bad("timezone", "unknown timezone", in.Timezone)
	}
	if len(details) > 0 {
		return HouseholdSettings{}, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}

	var autoApprove sql.NullInt64
	if in.AutoApproveHours != nil {
		autoApprove = sql.NullInt64{Int64: int64(*in.AutoApproveHours), Valid: true}
	}
	var negative int64
	if in.AllowNegativeBalance {
		negative = 1
	}
	if in.StreakMilestones == nil {
		in.StreakMilestones = []StreakMilestone{} // stored as [], never null
	}
	milestones, err := json.Marshal(in.StreakMilestones)
	if err != nil {
		return HouseholdSettings{}, err
	}
	write := func(ctx context.Context, q *sqlc.Queries, emit func(events.Event) error) error {
		before, err := q.GetHousehold(ctx)
		if err != nil {
			return err
		}
		if err := checkKidDayEnds(ctx, q, dayEnd); err != nil {
			return err
		}
		if err := q.UpdateHousehold(ctx, sqlc.UpdateHouseholdParams{
			Name: in.Name, Timezone: in.Timezone, DayEnd: dayEnd.String(), WeekStart: int64(weekStart),
			AllowNegativeBalance: negative, SiblingVisibility: in.SiblingVisibility,
			MediaRetentionDays: int64(in.MediaRetentionDays), AutoApproveHours: autoApprove,
			OfflineGraceMinutes: int64(in.OfflineGraceMinutes), DefaultChoreMinutes: int64(in.DefaultChoreMinutes),
			StreakMilestones: string(milestones),
		}); err != nil {
			return err
		}
		if before.Timezone != in.Timezone || before.DayEnd != dayEnd.String() || before.WeekStart != int64(weekStart) {
			return emit(CalendarChanged{Timezone: in.Timezone, DayEnd: dayEnd.String(), WeekStart: in.WeekStart})
		}
		return nil
	}

	if s.bus != nil {
		err = s.bus.InTx(ctx, func(tx *events.Tx) error {
			return write(ctx, tx.Q, func(ev events.Event) error { return tx.Emit(ctx, ev) })
		})
	} else {
		err = write(ctx, s.db.Queries, func(events.Event) error { return nil })
	}
	if err != nil {
		return HouseholdSettings{}, err
	}
	return s.Get(ctx)
}

// checkKidDayEnds refuses a household day end earlier than any kid's override.
func checkKidDayEnds(ctx context.Context, q *sqlc.Queries, dayEnd caltime.TimeOfDay) error {
	kids, err := q.ListKids(ctx, false)
	if err != nil {
		return err
	}
	var later []string
	for _, k := range kids {
		if !k.DayEndOverride.Valid {
			continue
		}
		if o, err := caltime.ParseTimeOfDay(k.DayEndOverride.String); err == nil && o.DayEndMinutes() > dayEnd.DayEndMinutes() {
			later = append(later, fmt.Sprintf("%s (%s)", k.DisplayName, o))
		}
	}
	if len(later) > 0 {
		msg := "these kids' days end later; change their day end first: " + strings.Join(later, ", ")
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, msg,
			&huma.ErrorDetail{Location: "body.dayEnd", Message: msg, Value: dayEnd.String()})
	}
	return nil
}
