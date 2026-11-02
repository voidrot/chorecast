package horizon

import (
	"context"
	"time"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/household"
)

// ChoreInstance is one due instance of a chore as the API returns it.
type ChoreInstance struct {
	ID                 int64                   `json:"id"`
	ChoreID            int64                   `json:"choreId"`
	ChoreName          string                  `json:"choreName"`
	KidID              int64                   `json:"kidId,omitempty" doc:"The assignee; absent for a shared instance"`
	GroupKidIDs        []int64                 `json:"groupKidIds,omitempty" doc:"Shared: the members who may complete it"`
	Date               caltime.Date            `json:"date" format:"date" doc:"The day it belongs to (flexible: the week's first active day)"`
	Slot               string                  `json:"slot,omitempty" doc:"HH:MM due time; absent = due at day close (or week close when flexible)"`
	Seq                int                     `json:"seq,omitempty" doc:"Flexible: which of the week's completions this is (0-based)"`
	DueAt              time.Time               `json:"dueAt"`
	Flexible           bool                    `json:"flexible,omitempty" doc:"One of a flexible chore's completions for the week"`
	Status             string                  `json:"status" enum:"open,submitted,approved,missed,excused"`
	Planned            bool                    `json:"planned" doc:"Dated after today: a forecast that may still change"`
	AutoAssigned       bool                    `json:"autoAssigned,omitempty" doc:"Picked by the fair auto-pick"`
	ManuallyReassigned bool                    `json:"manuallyReassigned,omitempty" doc:"A parent or a swap changed the assignee"`
	CompletedBy        int64                   `json:"completedBy,omitempty" doc:"The kid who completed it last (shared: the completer); kept after a rejection or an approval reversal"`
	CompletedAt        time.Time               `json:"completedAt,omitzero" doc:"When it was completed last"`
	RejectionCount     int                     `json:"rejectionCount,omitempty" doc:"How many times a parent rejected it"`
	LastRejection      *ChoreInstanceRejection `json:"lastRejection,omitempty" doc:"The latest rejection, if any"`
}

// ChoreInstanceRejection is a parent's feedback when rejecting a submitted instance.
type ChoreInstanceRejection struct {
	Reason     string    `json:"reason,omitempty" enum:"not_done,not_finished,needs_redo,evidence_unclear" doc:"The quick-pick reason"`
	Text       string    `json:"text,omitempty" doc:"The parent's own words"`
	KidID      int64     `json:"kidId,omitempty" doc:"The completer whose submission was rejected"`
	RejectedBy int64     `json:"rejectedBy,omitempty" doc:"The parent who rejected it"`
	RejectedAt time.Time `json:"rejectedAt"`
}

// ListFilter selects instances: dates [From, To] and one kid (0 = every kid).
type ListFilter struct {
	KidID    int64
	From, To caltime.Date
}

// Range returns the default list range, [today, today+Days], in the household calendar.
func (g *Generator) Range(ctx context.Context) (caltime.Date, caltime.Date, error) {
	cal, err := household.Calendar(ctx, g.db.Queries)
	if err != nil {
		return caltime.Date{}, caltime.Date{}, err
	}
	today := Today(cal, g.clock.Now())
	return today, today.AddDays(Days), nil
}

// List returns the instances matching f, by date and due time. A kid's list includes the shared
// instances they're a member of.
func (g *Generator) List(ctx context.Context, f ListFilter) ([]ChoreInstance, error) {
	cal, err := household.Calendar(ctx, g.db.Queries)
	if err != nil {
		return nil, err
	}
	today := Today(cal, g.clock.Now())
	rows, err := g.db.ListChoreInstancesBetween(ctx, sqlc.ListChoreInstancesBetweenParams{
		FromDate: f.From.String(), ToDate: f.To.String(), Kid: f.KidID,
	})
	if err != nil {
		return nil, err
	}
	memberRows, err := g.db.ListChoreInstanceMembersBetween(ctx, sqlc.ListChoreInstanceMembersBetweenParams{
		OccurrenceDate: f.From.String(), OccurrenceDate_2: f.To.String(),
	})
	if err != nil {
		return nil, err
	}
	members := map[int64][]int64{}
	for _, m := range memberRows {
		members[m.InstanceID] = append(members[m.InstanceID], m.KidID)
	}
	rejections, err := g.db.ListLatestChoreInstanceRejectionsBetween(ctx, sqlc.ListLatestChoreInstanceRejectionsBetweenParams{
		OccurrenceDate: f.From.String(), OccurrenceDate_2: f.To.String(),
	})
	if err != nil {
		return nil, err
	}
	latest := map[int64]sqlc.ChoreInstanceRejection{}
	for _, r := range rejections {
		latest[r.InstanceID] = r
	}
	out := make([]ChoreInstance, 0, len(rows))
	for _, r := range rows {
		var last *sqlc.ChoreInstanceRejection
		if rej, ok := latest[r.ChoreInstance.ID]; ok {
			last = &rej
		}
		out = append(out, toInstance(r.ChoreInstance, r.ChoreName, members[r.ChoreInstance.ID], last, today))
	}
	return out, nil
}

// Instance loads one instance as the API returns it; sql.ErrNoRows if there is none. q may be a
// transaction's queries.
func Instance(ctx context.Context, q *sqlc.Queries, now time.Time, id int64) (ChoreInstance, error) {
	row, err := q.GetChoreInstance(ctx, id)
	if err != nil {
		return ChoreInstance{}, err
	}
	cal, err := household.Calendar(ctx, q)
	if err != nil {
		return ChoreInstance{}, err
	}
	members, err := q.ListChoreInstanceMembers(ctx, id)
	if err != nil {
		return ChoreInstance{}, err
	}
	var last *sqlc.ChoreInstanceRejection
	if row.ChoreInstance.RejectionCount > 0 {
		r, err := q.LatestChoreInstanceRejection(ctx, id)
		if err != nil {
			return ChoreInstance{}, err
		}
		last = &r
	}
	return toInstance(row.ChoreInstance, row.ChoreName, members, last, Today(cal, now)), nil
}

func toInstance(r sqlc.ChoreInstance, choreName string, members []int64, last *sqlc.ChoreInstanceRejection, today caltime.Date) ChoreInstance {
	d, _ := caltime.ParseDate(r.OccurrenceDate)
	due, _ := db.ParseTimestamp(r.DueAt)
	out := ChoreInstance{
		ID: r.ID, ChoreID: r.ChoreID, ChoreName: choreName, KidID: r.KidID.Int64, GroupKidIDs: members,
		Date: d, Slot: r.Slot, Seq: int(r.Seq), DueAt: due, Flexible: r.Flexible == 1, Status: r.Status,
		Planned: d.After(today), AutoAssigned: r.AutoAssigned == 1, ManuallyReassigned: r.ManuallyReassigned == 1,
		CompletedBy: r.CompletedBy.Int64, RejectionCount: int(r.RejectionCount),
	}
	if r.CompletedAt.Valid {
		out.CompletedAt, _ = db.ParseTimestamp(r.CompletedAt.String)
	}
	if last != nil {
		at, _ := db.ParseTimestamp(last.CreatedAt)
		out.LastRejection = &ChoreInstanceRejection{
			Reason: last.ReasonCode, Text: last.ReasonText, KidID: last.KidID.Int64, RejectedBy: last.RejectedBy.Int64, RejectedAt: at,
		}
	}
	return out
}
