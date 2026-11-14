package chores

import (
	"bytes"
	"context"
	_ "embed" // the starter pack is data in the binary
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

// StarterPack is the curated set of chores, locations and equipment a new household can add in one
// step. The same types are the shape of starter/en.json and of the GET response; the file never sets
// Exists.
type StarterPack struct {
	Categories []StarterCategory    `json:"categories" nullable:"false"`
	Locations  []StarterCatalogItem `json:"locations" nullable:"false"`
	Equipment  []StarterCatalogItem `json:"equipment" nullable:"false"`
	Chores     []StarterChore       `json:"chores" nullable:"false"`
}

// StarterCategory groups chores on the review screen.
type StarterCategory struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// StarterCatalogItem is a location or equipment a starter chore uses.
type StarterCatalogItem struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Exists      bool   `json:"exists" doc:"One with this name (any case) is already in the catalog; it is reused"`
}

// StarterChore is a suggested chore.
type StarterChore struct {
	Key              string                `json:"key"`
	Category         string                `json:"category" doc:"Category key"`
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	Points           int                   `json:"points"`
	EstimatedMinutes *int                  `json:"estimatedMinutes,omitempty"`
	MinAge           *int                  `json:"minAge,omitempty"`
	Steps            []string              `json:"steps,omitempty"`
	Schedule         schedule.ScheduleRule `json:"schedule" doc:"startDate is set to today when applied"`
	Assignment       string                `json:"assignment" enum:"everyone,auto"`
	RequiresApproval bool                  `json:"requiresApproval"`
	Evidence         EvidenceRules         `json:"evidence"`
	Location         string                `json:"location,omitempty" doc:"Location key"`
	Equipment        []string              `json:"equipment,omitempty" doc:"Equipment keys"`
	Recommended      bool                  `json:"recommended" doc:"Ticked by default"`
	Exists           bool                  `json:"exists" doc:"A chore with this name (any case) exists; applying skips it"`
}

// StarterApplyInput selects the starter chores to add.
type StarterApplyInput struct {
	Chores   []string `json:"chores" minItems:"1" maxItems:"200" uniqueItems:"true" doc:"Starter chore keys"`
	Disabled bool     `json:"disabled,omitempty" doc:"Add the chores paused (not scheduled)"`
}

// StarterCreatedItem is a row an apply created.
type StarterCreatedItem struct {
	Key  string `json:"key"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// StarterCreated lists what an apply created.
type StarterCreated struct {
	Locations []StarterCreatedItem `json:"locations" nullable:"false"`
	Equipment []StarterCreatedItem `json:"equipment" nullable:"false"`
	Chores    []StarterCreatedItem `json:"chores" nullable:"false"`
}

// StarterApplyResult is what an apply did.
type StarterApplyResult struct {
	Created StarterCreated `json:"created"`
	Skipped []string       `json:"skipped" nullable:"false" doc:"Chore keys skipped because a chore with that name exists"`
}

//go:embed starter/en.json
var starterJSON []byte

// loadStarterPack parses the embedded pack once. Callers must not modify what it returns.
var loadStarterPack = sync.OnceValues(func() (StarterPack, error) {
	dec := json.NewDecoder(bytes.NewReader(starterJSON))
	dec.DisallowUnknownFields()
	var p StarterPack
	if err := dec.Decode(&p); err != nil {
		return StarterPack{}, fmt.Errorf("starter pack: %w", err)
	}
	return p, nil
})

// nameKey is how names are matched, for the preview and the apply alike.
func nameKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// starterIndex is what the household already has, keyed by nameKey.
type starterIndex struct {
	chores    map[string]bool
	locations map[string]int64 // the lowest id when names differ only by case
	equipment map[string]int64
}

// loadStarterIndex reads through q, so the apply can run it on its transaction's querier.
func loadStarterIndex(ctx context.Context, q *sqlc.Queries) (starterIndex, error) {
	idx := starterIndex{chores: map[string]bool{}, locations: map[string]int64{}, equipment: map[string]int64{}}
	chores, err := q.ListChores(ctx)
	if err != nil {
		return idx, err
	}
	for _, c := range chores {
		idx.chores[nameKey(c.Name)] = true // disabled chores count too
	}
	locs, err := q.ListLocations(ctx)
	if err != nil {
		return idx, err
	}
	for _, l := range locs {
		lowestID(idx.locations, nameKey(l.Name), l.ID)
	}
	eq, err := q.ListEquipment(ctx)
	if err != nil {
		return idx, err
	}
	for _, e := range eq {
		lowestID(idx.equipment, nameKey(e.Name), e.ID)
	}
	return idx, nil
}

func lowestID(m map[string]int64, key string, id int64) {
	if old, ok := m[key]; !ok || id < old {
		m[key] = id
	}
}

// StarterPack returns the pack, marking what the household already has.
func (s *Service) StarterPack(ctx context.Context) (StarterPack, error) {
	pack, err := loadStarterPack()
	if err != nil {
		return StarterPack{}, err
	}
	idx, err := loadStarterIndex(ctx, s.db.Queries)
	if err != nil {
		return StarterPack{}, err
	}
	out := StarterPack{
		Categories: append([]StarterCategory{}, pack.Categories...),
		Locations:  append([]StarterCatalogItem{}, pack.Locations...),
		Equipment:  append([]StarterCatalogItem{}, pack.Equipment...),
		Chores:     make([]StarterChore, 0, len(pack.Chores)),
	}
	for i := range out.Locations {
		_, out.Locations[i].Exists = idx.locations[nameKey(out.Locations[i].Name)]
	}
	for i := range out.Equipment {
		_, out.Equipment[i].Exists = idx.equipment[nameKey(out.Equipment[i].Name)]
	}
	for _, c := range pack.Chores {
		c.Steps = slices.Clone(c.Steps)
		c.Equipment = slices.Clone(c.Equipment)
		c.Schedule = cloneRule(c.Schedule)
		c.Exists = idx.chores[nameKey(c.Name)]
		out.Chores = append(out.Chores, c)
	}
	return out, nil
}

// cloneRule copies the slices a rule shares with the cached pack (schedule.Normalize may reorder them).
func cloneRule(r schedule.ScheduleRule) schedule.ScheduleRule {
	r.Weekdays = slices.Clone(r.Weekdays)
	r.MonthDays = slices.Clone(r.MonthDays)
	if r.NthWeekday != nil {
		n := *r.NthWeekday
		r.NthWeekday = &n
	}
	return r
}

// ApplyStarter adds the selected chores, and the locations and equipment they use, in one
// transaction with one chore.changed event. A chore whose name exists (any case, disabled too) is
// skipped; a location or equipment whose name exists is reused. Repeating a call creates nothing.
func (s *Service) ApplyStarter(ctx context.Context, in StarterApplyInput) (StarterApplyResult, error) {
	pack, err := loadStarterPack()
	if err != nil {
		return StarterApplyResult{}, err
	}
	choreByKey := make(map[string]StarterChore, len(pack.Chores))
	for _, c := range pack.Chores {
		choreByKey[c.Key] = c
	}
	var unknown []error
	for i, key := range in.Chores {
		if _, ok := choreByKey[key]; !ok {
			unknown = append(unknown, &huma.ErrorDetail{Location: fmt.Sprintf("body.chores[%d]", i), Message: "unknown starter chore", Value: key})
		}
	}
	if len(unknown) > 0 {
		return StarterApplyResult{}, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", unknown...)
	}

	cal, err := household.Calendar(ctx, s.db.Queries)
	if err != nil {
		return StarterApplyResult{}, err
	}
	today := cal.CalendarDate(s.clock.Now())
	now := db.Timestamp(s.clock.Now())

	res := StarterApplyResult{
		Created: StarterCreated{Locations: []StarterCreatedItem{}, Equipment: []StarterCreatedItem{}, Chores: []StarterCreatedItem{}},
		Skipped: []string{},
	}
	err = s.inTx(ctx, func(q *sqlc.Queries, emit func(events.Event) error) error {
		// Inside the transaction only q may touch the database: the pool has one connection.
		idx, err := loadStarterIndex(ctx, q)
		if err != nil {
			return err
		}
		for _, key := range in.Chores {
			c := choreByKey[key]
			if idx.chores[nameKey(c.Name)] {
				res.Skipped = append(res.Skipped, key)
				continue
			}
			input := ChoreInput{
				Name: c.Name, Description: c.Description, Points: c.Points, EstimatedMinutes: c.EstimatedMinutes,
				MinAge: c.MinAge, Steps: slices.Clone(c.Steps), Assignment: &ChoreAssignment{Mode: c.Assignment},
				RequiresApproval: c.RequiresApproval, Evidence: c.Evidence, Disabled: in.Disabled,
			}
			rule := cloneRule(c.Schedule)
			rule.StartDate = today
			input.Schedule = &rule
			if c.Location != "" {
				id, err := resolveStarterRef(c.Location, pack.Locations, idx.locations, &res.Created.Locations,
					func(name, desc string) (int64, error) {
						l, err := q.CreateLocation(ctx, sqlc.CreateLocationParams{Name: name, Description: desc, CreatedAt: now})
						return l.ID, err
					})
				if err != nil {
					return err
				}
				input.LocationID = &id
			}
			for _, eqKey := range c.Equipment {
				id, err := resolveStarterRef(eqKey, pack.Equipment, idx.equipment, &res.Created.Equipment,
					func(name, desc string) (int64, error) {
						e, err := q.CreateEquipment(ctx, sqlc.CreateEquipmentParams{Name: name, Description: desc, CreatedAt: now})
						return e.ID, err
					})
				if err != nil {
					return err
				}
				input.EquipmentIDs = append(input.EquipmentIDs, id)
			}
			if input, err = normalize(ctx, q, input); err != nil {
				return err
			}
			id, err := insertChore(ctx, q, input, now)
			if err != nil {
				return err
			}
			idx.chores[nameKey(c.Name)] = true
			res.Created.Chores = append(res.Created.Chores, StarterCreatedItem{Key: key, ID: id, Name: input.Name})
		}
		if len(res.Created.Chores) == 0 {
			return nil
		}
		return emit(ChoreChanged{ChoreID: 0})
	})
	if err != nil {
		return StarterApplyResult{}, err
	}
	return res, nil
}

// resolveStarterRef returns the id of the catalog entry with the pack item's name, creating it with
// create (and recording it in created) when the household has none.
func resolveStarterRef(key string, items []StarterCatalogItem, have map[string]int64, created *[]StarterCreatedItem,
	create func(name, description string) (int64, error)) (int64, error) {
	i := slices.IndexFunc(items, func(it StarterCatalogItem) bool { return it.Key == key })
	if i < 0 {
		return 0, fmt.Errorf("starter pack: unknown catalog key %q", key)
	}
	item := items[i]
	if id, ok := have[nameKey(item.Name)]; ok {
		return id, nil
	}
	id, err := create(item.Name, item.Description)
	if err != nil {
		return 0, err
	}
	have[nameKey(item.Name)] = id
	*created = append(*created, StarterCreatedItem{Key: key, ID: id, Name: item.Name})
	return id, nil
}
