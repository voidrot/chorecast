package chores

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

// SchedulePreviewInput is a rule and due times to preview, before or while editing a chore.
type SchedulePreviewInput struct {
	Rule  schedule.ScheduleRule `json:"rule"`
	Slots []string              `json:"slots,omitempty" maxItems:"6" doc:"Due times (HH:MM); empty = due at day close"`
	Count int                   `json:"count,omitempty" minimum:"0" maximum:"50" doc:"How many occurrences (default 10)"`
}

// SchedulePreview is the normalized rule and its next occurrences from today.
type SchedulePreview struct {
	Rule        schedule.ScheduleRule         `json:"rule"`
	Occurrences []schedule.ScheduleOccurrence `json:"occurrences" nullable:"false"`
}

// PreviewSchedule validates a rule and lists its next occurrences from today, in the household
// timezone, without saving anything.
func (s *Service) PreviewSchedule(ctx context.Context, in SchedulePreviewInput) (SchedulePreview, error) {
	r := schedule.Normalize(in.Rule)
	details := schedule.Validate(r, "body.rule")
	var slots []caltime.TimeOfDay
	for i, sl := range in.Slots {
		t, err := caltime.ParseTimeOfDay(sl)
		if err != nil {
			details = append(details, &huma.ErrorDetail{Location: fmt.Sprintf("body.slots[%d]", i), Message: "use HH:MM", Value: sl})
			continue
		}
		slots = append(slots, t)
	}
	if len(details) > 0 {
		return SchedulePreview{}, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}
	cal, err := household.Calendar(ctx, s.db.Queries)
	if err != nil {
		return SchedulePreview{}, err
	}
	n := in.Count
	if n == 0 {
		n = 10
	}
	occ := schedule.Next(r, cal, sortSlots(slots), cal.CalendarDate(s.clock.Now()), n)
	if occ == nil {
		occ = []schedule.ScheduleOccurrence{}
	}
	return SchedulePreview{Rule: r, Occurrences: occ}, nil
}

func sortSlots(v []caltime.TimeOfDay) []caltime.TimeOfDay {
	slices.SortFunc(v, func(a, b caltime.TimeOfDay) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(v)
}
