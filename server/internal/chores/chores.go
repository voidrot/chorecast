// Package chores holds chore definitions: what the chore is (name, steps, points, penalty,
// estimated time, minimum age), when it's due in a day (slots), how it's checked (approval,
// evidence, auto-approve override, escalation) and where/with what (location, equipment).
// The schedule rule (which days, internal/schedule) is stored on the chore; assignment (who)
// builds on these. See docs/features/schedules.md.
package chores

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/assign"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

const (
	maxSteps = 20
	maxSlots = 6
)

// EvidenceRules is what a kid must provide when completing the chore.
type EvidenceRules struct {
	Photos      int  `json:"photos" minimum:"0" maximum:"5" doc:"Minimum number of photos (taken in the app)"`
	BeforeAfter bool `json:"beforeAfter,omitempty" doc:"The photos include a before and an after (needs photos >= 2)"`
	Video       bool `json:"video,omitempty" doc:"A short video is required"`
	Checklist   bool `json:"checklist,omitempty" doc:"Every step must be ticked (needs steps)"`
	Note        bool `json:"note,omitempty" doc:"A written note is required"`
}

// ChoreEligibility is a per-kid exception to the minimum age.
type ChoreEligibility struct {
	KidID int64 `json:"kidId"`
	Allow bool  `json:"allow" doc:"true: allowed even if younger than minAge; false: never gets this chore"`
}

// ChoreAssignment is who gets each occurrence. See internal/assign.
type ChoreAssignment struct {
	Mode          string  `json:"mode" enum:"fixed,everyone,rotation,auto,shared" doc:"fixed: each listed kid; everyone: each eligible kid; rotation: one kid, taking turns; auto: the eligible kid with the least work over 7 days; shared: one instance for the group"`
	KidIDs        []int64 `json:"kidIds,omitempty" maxItems:"50" uniqueItems:"true" doc:"fixed: the kids; rotation: the turn order; auto: the pool (empty = every kid); shared: the group. Ignored for everyone."`
	SharedPoints  string  `json:"sharedPoints,omitempty" enum:"completer_takes_all,split_evenly" doc:"shared: points to the completer, or split evenly with the helpers they tag (remainder to the completer)"`
	SharedPenalty string  `json:"sharedPenalty,omitempty" enum:"all,none" doc:"shared: when missed, every member gets the penalty, or nobody"`
}

// ChoreInput is a chore as parents create or edit it.
type ChoreInput struct {
	Name             string                 `json:"name" minLength:"1" maxLength:"80"`
	Description      string                 `json:"description,omitempty" maxLength:"2000"`
	Points           int                    `json:"points" minimum:"0" maximum:"100000"`
	Penalty          int                    `json:"penalty,omitempty" minimum:"0" maximum:"100000" doc:"Points deducted when the chore is missed"`
	EstimatedMinutes *int                   `json:"estimatedMinutes,omitempty" minimum:"1" maximum:"1440"`
	MinAge           *int                   `json:"minAge,omitempty" minimum:"1" maximum:"25"`
	Steps            []string               `json:"steps,omitempty" maxItems:"20" doc:"Checklist steps shown to the kid"`
	Slots            []string               `json:"slots,omitempty" maxItems:"6" doc:"Due times (HH:MM) each day it occurs; empty = due at day close. Each slot is its own instance."`
	Schedule         *schedule.ScheduleRule `json:"schedule,omitempty" doc:"Which days it occurs; absent = not scheduled (kept for later or one-off assignment)"`
	Assignment       *ChoreAssignment       `json:"assignment,omitempty" doc:"Who gets it; absent = everyone eligible"`
	RequiresApproval bool                   `json:"requiresApproval" doc:"A parent approves before points are credited"`
	Escalate         bool                   `json:"escalate,omitempty" doc:"Remind parents if still open 30 minutes before day close (meds, pets)"`
	AutoApproveHours *int                   `json:"autoApproveHours,omitempty" minimum:"0" maximum:"720" doc:"Absent = household default; 0 = never; N = approve after N hours"`
	Evidence         EvidenceRules          `json:"evidence"`
	LocationID       *int64                 `json:"locationId,omitempty"`
	EquipmentIDs     []int64                `json:"equipmentIds,omitempty" uniqueItems:"true"`
	Eligibility      []ChoreEligibility     `json:"eligibility,omitempty" doc:"Per-kid exceptions to minAge"`
	Disabled         bool                   `json:"disabled,omitempty" doc:"Kept, but not scheduled"`
}

// Chore is a chore definition as the API returns it.
type Chore struct {
	ID int64 `json:"id"`
	ChoreInput
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Service manages chore definitions and the location/equipment catalogs.
type Service struct {
	db    *db.DB
	clock clock.Clock
}

func New(d *db.DB, c clock.Clock) *Service { return &Service{db: d, clock: c} }

var errNotFound = apierr.New(http.StatusNotFound, apierr.CodeNotFound, "no such chore")

func optInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func nullInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func toChore(c sqlc.Chore, equipment []int64, elig []ChoreEligibility) Chore {
	var steps, slots []string
	_ = json.Unmarshal([]byte(c.Steps), &steps)
	_ = json.Unmarshal([]byte(c.Slots), &slots)
	var rule *schedule.ScheduleRule
	if c.Schedule.Valid {
		var r schedule.ScheduleRule
		if json.Unmarshal([]byte(c.Schedule.String), &r) == nil {
			rule = &r
		}
	}
	created, _ := db.ParseTimestamp(c.CreatedAt)
	updated, _ := db.ParseTimestamp(c.UpdatedAt)
	var loc *int64
	if c.LocationID.Valid {
		loc = &c.LocationID.Int64
	}
	return Chore{
		ID: c.ID,
		ChoreInput: ChoreInput{
			Name: c.Name, Description: c.Description, Points: int(c.Points), Penalty: int(c.Penalty),
			EstimatedMinutes: optInt(c.EstimatedMinutes), MinAge: optInt(c.MinAge), Steps: steps, Slots: slots,
			Schedule:         rule,
			Assignment:       toAssignment(c),
			RequiresApproval: c.RequiresApproval == 1, Escalate: c.Escalate == 1, AutoApproveHours: optInt(c.AutoApproveHours),
			Evidence: EvidenceRules{
				Photos: int(c.EvidencePhotos), BeforeAfter: c.EvidenceBeforeAfter == 1, Video: c.EvidenceVideo == 1,
				Checklist: c.EvidenceChecklist == 1, Note: c.EvidenceNote == 1,
			},
			LocationID: loc, EquipmentIDs: equipment, Eligibility: elig, Disabled: c.Disabled == 1,
		},
		CreatedAt: created, UpdatedAt: updated,
	}
}

// List returns every chore, enabled first, by name.
func (s *Service) List(ctx context.Context) ([]Chore, error) {
	rows, err := s.db.ListChores(ctx)
	if err != nil {
		return nil, err
	}
	equipment, elig, err := s.links(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Chore, 0, len(rows))
	for _, c := range rows {
		out = append(out, toChore(c, nonNil(equipment[c.ID]), nonNil(elig[c.ID])))
	}
	return out, nil
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (s *Service) links(ctx context.Context) (map[int64][]int64, map[int64][]ChoreEligibility, error) {
	eqRows, err := s.db.ListChoreEquipment(ctx)
	if err != nil {
		return nil, nil, err
	}
	equipment := map[int64][]int64{}
	for _, r := range eqRows {
		equipment[r.ChoreID] = append(equipment[r.ChoreID], r.EquipmentID)
	}
	elRows, err := s.db.ListChoreEligibility(ctx)
	if err != nil {
		return nil, nil, err
	}
	elig := map[int64][]ChoreEligibility{}
	for _, r := range elRows {
		elig[r.ChoreID] = append(elig[r.ChoreID], ChoreEligibility{KidID: r.KidID, Allow: r.Allow == 1})
	}
	return equipment, elig, nil
}

// Get returns one chore.
func (s *Service) Get(ctx context.Context, id int64) (Chore, error) {
	c, err := s.db.GetChore(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Chore{}, errNotFound
	}
	if err != nil {
		return Chore{}, err
	}
	equipment, elig, err := s.links(ctx)
	if err != nil {
		return Chore{}, err
	}
	return toChore(c, nonNil(equipment[id]), nonNil(elig[id])), nil
}

// normalize trims and checks what the schema can't, returning the cleaned input.
func (s *Service) normalize(ctx context.Context, in ChoreInput) (ChoreInput, error) {
	var details []error
	bad := func(loc, msg string, v any) {
		details = append(details, &huma.ErrorDetail{Location: "body." + loc, Message: msg, Value: v})
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" {
		bad("name", "a name is required", in.Name)
	}

	steps := make([]string, 0, len(in.Steps))
	for i, st := range in.Steps {
		st = strings.TrimSpace(st)
		switch {
		case st == "":
			continue
		case len(st) > 200:
			bad(fmt.Sprintf("steps[%d]", i), "steps are at most 200 characters", st)
		}
		steps = append(steps, st)
	}
	if len(steps) > maxSteps {
		bad("steps", fmt.Sprintf("at most %d steps", maxSteps), len(steps))
	}
	in.Steps = steps

	slots := make([]string, 0, len(in.Slots))
	for i, sl := range in.Slots {
		t, err := caltime.ParseTimeOfDay(sl)
		if err != nil {
			bad(fmt.Sprintf("slots[%d]", i), "use HH:MM", sl)
			continue
		}
		if !slices.Contains(slots, t.String()) {
			slots = append(slots, t.String())
		}
	}
	slices.Sort(slots)
	if len(slots) > maxSlots {
		bad("slots", fmt.Sprintf("at most %d due times a day", maxSlots), len(slots))
	}
	in.Slots = slots

	if in.Schedule != nil {
		r := schedule.Normalize(*in.Schedule)
		in.Schedule = &r
		details = append(details, schedule.Validate(r, "body.schedule")...)
	}

	in.Assignment = normalizeAssignment(in.Assignment)
	need := map[string]int{assign.ModeFixed: 1, assign.ModeRotation: 1, assign.ModeShared: 2}[in.Assignment.Mode]
	if len(in.Assignment.KidIDs) < need {
		bad("assignment.kidIds", fmt.Sprintf("%s needs at least %d kid(s)", in.Assignment.Mode, need), len(in.Assignment.KidIDs))
	}
	for i, id := range in.Assignment.KidIDs {
		// Archived kids may stay listed (assignment skips them), so archiving a kid doesn't block edits.
		if _, err := s.db.GetKidByID(ctx, id); err != nil {
			bad(fmt.Sprintf("assignment.kidIds[%d]", i), "unknown kid", id)
		}
	}

	if in.Evidence.BeforeAfter && in.Evidence.Photos < 2 {
		bad("evidence.beforeAfter", "before and after needs at least 2 photos", in.Evidence.Photos)
	}
	if in.Evidence.Checklist && len(in.Steps) == 0 {
		bad("evidence.checklist", "a checklist needs steps", nil)
	}

	if in.LocationID != nil {
		locs, err := s.db.ListLocations(ctx)
		if err != nil {
			return in, err
		}
		if !slices.ContainsFunc(locs, func(l sqlc.Location) bool { return l.ID == *in.LocationID }) {
			bad("locationId", "unknown location", *in.LocationID)
		}
	}
	if len(in.EquipmentIDs) > 0 {
		eq, err := s.db.ListEquipment(ctx)
		if err != nil {
			return in, err
		}
		for i, id := range in.EquipmentIDs {
			if !slices.ContainsFunc(eq, func(e sqlc.Equipment) bool { return e.ID == id }) {
				bad(fmt.Sprintf("equipmentIds[%d]", i), "unknown equipment", id)
			}
		}
	}
	seen := map[int64]bool{}
	for i, e := range in.Eligibility {
		if seen[e.KidID] {
			bad(fmt.Sprintf("eligibility[%d]", i), "one exception per kid", e.KidID)
			continue
		}
		seen[e.KidID] = true
		if _, err := s.db.GetKidByID(ctx, e.KidID); err != nil {
			bad(fmt.Sprintf("eligibility[%d].kidId", i), "unknown kid", e.KidID)
		}
	}
	if len(details) > 0 {
		return in, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}
	return in, nil
}

func toAssignment(c sqlc.Chore) *ChoreAssignment {
	a := &ChoreAssignment{Mode: c.AssignMode}
	_ = json.Unmarshal([]byte(c.AssignKids), &a.KidIDs)
	if a.Mode == assign.ModeShared {
		a.SharedPoints, a.SharedPenalty = c.SharedPoints, c.SharedPenalty
	}
	return a
}

// normalizeAssignment fills defaults and drops what the mode doesn't use.
func normalizeAssignment(a *ChoreAssignment) *ChoreAssignment {
	if a == nil {
		return &ChoreAssignment{Mode: assign.ModeEveryone}
	}
	out := *a
	out.KidIDs = slices.Clone(a.KidIDs)
	if out.Mode == assign.ModeEveryone {
		out.KidIDs = nil
	}
	if out.Mode == assign.ModeShared {
		if out.SharedPoints == "" {
			out.SharedPoints = assign.PointsCompleterTakesAll
		}
		if out.SharedPenalty == "" {
			out.SharedPenalty = assign.PenaltyAll
		}
	} else {
		out.SharedPoints, out.SharedPenalty = "", ""
	}
	return &out
}

// assignmentColumns are the stored values; shared policies keep their defaults for other modes.
func assignmentColumns(a *ChoreAssignment) (mode, kids, points, penalty string) {
	b, _ := json.Marshal(nonNil(a.KidIDs))
	points, penalty = assign.PointsCompleterTakesAll, assign.PenaltyAll
	if a.SharedPoints != "" {
		points = a.SharedPoints
	}
	if a.SharedPenalty != "" {
		penalty = a.SharedPenalty
	}
	return a.Mode, string(b), points, penalty
}

func scheduleJSON(r *schedule.ScheduleRule) sql.NullString {
	if r == nil {
		return sql.NullString{}
	}
	b, _ := json.Marshal(r)
	return sql.NullString{String: string(b), Valid: true}
}

func mustJSON(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Create adds a chore.
func (s *Service) Create(ctx context.Context, in ChoreInput) (Chore, error) {
	in, err := s.normalize(ctx, in)
	if err != nil {
		return Chore{}, err
	}
	now := db.Timestamp(s.clock.Now())
	mode, kids, points, penalty := assignmentColumns(in.Assignment)
	var id int64
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		c, err := q.CreateChore(ctx, sqlc.CreateChoreParams{
			Name: in.Name, Description: in.Description, Points: int64(in.Points), Penalty: int64(in.Penalty),
			EstimatedMinutes: nullInt(in.EstimatedMinutes), MinAge: nullInt(in.MinAge), Steps: mustJSON(in.Steps),
			Slots: mustJSON(in.Slots), Schedule: scheduleJSON(in.Schedule),
			AssignMode: mode, AssignKids: kids, SharedPoints: points, SharedPenalty: penalty, RequiresApproval: b2i(in.RequiresApproval), Escalate: b2i(in.Escalate),
			AutoApproveHours: nullInt(in.AutoApproveHours), EvidencePhotos: int64(in.Evidence.Photos),
			EvidenceBeforeAfter: b2i(in.Evidence.BeforeAfter), EvidenceVideo: b2i(in.Evidence.Video),
			EvidenceChecklist: b2i(in.Evidence.Checklist), EvidenceNote: b2i(in.Evidence.Note),
			LocationID: nullID(in.LocationID), Disabled: b2i(in.Disabled), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		id = c.ID
		return writeLinks(ctx, q, id, in)
	})
	if err != nil {
		return Chore{}, err
	}
	return s.Get(ctx, id)
}

// Update replaces a chore's definition. Optional fields left out are cleared.
func (s *Service) Update(ctx context.Context, id int64, in ChoreInput) (Chore, error) {
	old, err := s.db.GetChore(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Chore{}, errNotFound
	}
	if err != nil {
		return Chore{}, err
	}
	in, err = s.normalize(ctx, in)
	if err != nil {
		return Chore{}, err
	}
	mode, kids, points, penalty := assignmentColumns(in.Assignment)
	rotation := old.RotationState
	if mode != old.AssignMode || kids != old.AssignKids {
		rotation = "{}" // a different rotation starts from its first kid
	}
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		if err := q.UpdateChore(ctx, sqlc.UpdateChoreParams{
			Name: in.Name, Description: in.Description, Points: int64(in.Points), Penalty: int64(in.Penalty),
			EstimatedMinutes: nullInt(in.EstimatedMinutes), MinAge: nullInt(in.MinAge), Steps: mustJSON(in.Steps),
			Slots: mustJSON(in.Slots), Schedule: scheduleJSON(in.Schedule),
			AssignMode: mode, AssignKids: kids, SharedPoints: points, SharedPenalty: penalty, RequiresApproval: b2i(in.RequiresApproval), Escalate: b2i(in.Escalate),
			AutoApproveHours: nullInt(in.AutoApproveHours), EvidencePhotos: int64(in.Evidence.Photos),
			EvidenceBeforeAfter: b2i(in.Evidence.BeforeAfter), EvidenceVideo: b2i(in.Evidence.Video),
			EvidenceChecklist: b2i(in.Evidence.Checklist), EvidenceNote: b2i(in.Evidence.Note),
			LocationID: nullID(in.LocationID), Disabled: b2i(in.Disabled), RotationState: rotation, UpdatedAt: db.Timestamp(s.clock.Now()), ID: id,
		}); err != nil {
			return err
		}
		if err := q.DeleteChoreEquipment(ctx, id); err != nil {
			return err
		}
		if err := q.DeleteChoreEligibility(ctx, id); err != nil {
			return err
		}
		return writeLinks(ctx, q, id, in)
	})
	if err != nil {
		return Chore{}, err
	}
	return s.Get(ctx, id)
}

func nullID(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func writeLinks(ctx context.Context, q *sqlc.Queries, id int64, in ChoreInput) error {
	for _, e := range in.EquipmentIDs {
		if err := q.AddChoreEquipment(ctx, sqlc.AddChoreEquipmentParams{ChoreID: id, EquipmentID: e}); err != nil {
			return err
		}
	}
	for _, e := range in.Eligibility {
		if err := q.AddChoreEligibility(ctx, sqlc.AddChoreEligibilityParams{ChoreID: id, KidID: e.KidID, Allow: b2i(e.Allow)}); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a chore. Once chores have history (instances, #23+), parents disable instead.
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.db.DeleteChore(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	return nil
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
