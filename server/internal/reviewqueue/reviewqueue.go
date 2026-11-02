// Package reviewqueue lists the submitted chores waiting for a parent's decision, each with what
// the kid submitted. Approving and rejecting are the instance transitions (internal/instances);
// this package only reads. Spec: docs/features/approvals-evidence.md (Review queue).
package reviewqueue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/evidence"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/paging"
)

// Media filter values.
const (
	MediaAny     = -1
	MediaWith    = 1
	MediaWithout = 0
)

// Filter narrows the queue. Zero ids match everything; Media is MediaAny, MediaWith or MediaWithout
// (the latest submission has photos, or has none).
type Filter struct {
	KidID, ChoreID int64
	Media          int
}

// ReviewQueueStep is one step of the chore's checklist and whether the latest submission ticked it.
type ReviewQueueStep struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// ReviewQueueItem is one submitted instance with its latest submission.
type ReviewQueueItem struct {
	Instance   horizon.ChoreInstance      `json:"instance"`
	KidName    string                     `json:"kidName" doc:"The kid who completed it (a shared chore's completer), else its assignee"`
	Steps      []ReviewQueueStep          `json:"steps" nullable:"false" doc:"The chore's checklist steps, with those the latest submission ticked; empty when it has none"`
	Submission *evidence.InstanceEvidence `json:"submission,omitempty" doc:"The latest submission: note, photos (signed thumbnails) and when it was submitted"`
}

type cursor struct {
	At string `json:"a"`
	ID int64  `json:"i"`
}

// List returns one page of the queue, oldest submission first (ties by instance id), so a client
// groups the flat list by kid. It takes a fixed number of queries whatever the page size.
func List(ctx context.Context, q *sqlc.Queries, clk clock.Clock, f Filter, p paging.Params, sign evidence.Signer) (paging.Page[ReviewQueueItem], error) {
	after, _, err := paging.Decode[cursor](p.Cursor)
	if err != nil {
		return paging.Page[ReviewQueueItem]{}, err
	}
	size := p.Size()
	rows, err := q.ListReviewQueue(ctx, sqlc.ListReviewQueueParams{
		Kid: f.KidID, Chore: f.ChoreID, Media: int64(f.Media), AfterAt: after.At, AfterID: after.ID, RowLimit: int64(size + 1),
	})
	if err != nil {
		return paging.Page[ReviewQueueItem]{}, fmt.Errorf("list review queue: %w", err)
	}
	page, err := paging.Build(rows, size, func(r sqlc.ListReviewQueueRow) cursor {
		return cursor{At: r.ChoreInstance.CompletedAt.String, ID: r.ChoreInstance.ID}
	})
	if err != nil {
		return paging.Page[ReviewQueueItem]{}, err
	}
	items, err := build(ctx, q, clk, page.Items, sign)
	if err != nil {
		return paging.Page[ReviewQueueItem]{}, err
	}
	return paging.Page[ReviewQueueItem]{Items: items, NextCursor: page.NextCursor}, nil
}

func build(ctx context.Context, q *sqlc.Queries, clk clock.Clock, rows []sqlc.ListReviewQueueRow, sign evidence.Signer) ([]ReviewQueueItem, error) {
	items := make([]ReviewQueueItem, 0, len(rows))
	if len(rows) == 0 {
		return items, nil
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ChoreInstance.ID
	}
	cal, err := household.Calendar(ctx, q)
	if err != nil {
		return nil, err
	}
	today := horizon.Today(cal, clk.Now())
	memberRows, err := q.ListChoreInstanceMembersByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	members := map[int64][]int64{}
	for _, m := range memberRows {
		members[m.InstanceID] = append(members[m.InstanceID], m.KidID)
	}
	rejectionRows, err := q.ListLatestChoreInstanceRejectionsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list rejections: %w", err)
	}
	rejections := map[int64]sqlc.ChoreInstanceRejection{}
	for _, r := range rejectionRows {
		rejections[r.InstanceID] = r
	}
	submissions, err := evidence.ListLatest(ctx, q, ids, sign)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		in := r.ChoreInstance
		var last *sqlc.ChoreInstanceRejection
		if rej, ok := rejections[in.ID]; ok {
			last = &rej
		}
		item := ReviewQueueItem{
			Instance: horizon.ToInstance(in, r.ChoreName, members[in.ID], last, today), KidName: r.KidName,
		}
		var done []int
		if sub, ok := submissions[in.ID]; ok {
			item.Submission, done = &sub, sub.Checklist
		}
		if item.Steps, err = steps(r.ChoreSteps, done); err != nil {
			return nil, fmt.Errorf("chore %d steps: %w", in.ChoreID, err)
		}
		items = append(items, item)
	}
	return items, nil
}

// steps resolves the ticked indexes against the chore's step texts.
func steps(raw string, done []int) ([]ReviewQueueStep, error) {
	var texts []string
	if err := json.Unmarshal([]byte(raw), &texts); err != nil {
		return nil, err
	}
	out := make([]ReviewQueueStep, len(texts))
	for i, t := range texts {
		out[i] = ReviewQueueStep{Text: t}
	}
	for _, i := range done {
		if i >= 0 && i < len(out) {
			out[i].Done = true
		}
	}
	return out, nil
}
