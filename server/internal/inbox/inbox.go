// Package inbox is the in-app notification inbox (docs/features/notifications.md). Domain events
// become one notifications row per recipient:
//
//   - kids get chore_approved (also when the system auto-approved it), chore_rejected (with the
//     reason), chore_missed, chore_excused and streak_milestone;
//   - every parent but the one who acted gets chore_submitted when a chore needs review.
//
// A shared chore's event lists all its members, and each gets one. Rows store the kind, what it
// refers to and the details (chore name, reason, streak length), never display text: clients
// render it. Auto-approvals don't notify parents; the daily summary digest will cover them.
//
// Handlers run as jobs after the commit and are delivered at least once. A row's unique key
// includes the event id, so a redelivered event writes nothing. Rows are purged after Keep.
package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/ledger"
	"github.com/voidrot/chorecast/server/internal/paging"
	"github.com/voidrot/chorecast/server/internal/streaks"
)

// Keep is how long notifications are kept before maintenance purges them. The spec names no
// period; it's long enough for a kid who was away to catch up.
const Keep = 90 * 24 * time.Hour

// Notification kinds.
const (
	KindChoreSubmitted  = "chore_submitted"  // parents: a chore waits for review
	KindChoreApproved   = "chore_approved"   // kids
	KindChoreRejected   = "chore_rejected"   // kids
	KindChoreMissed     = "chore_missed"     // kids
	KindChoreExcused    = "chore_excused"    // kids: a parent excused a missed chore
	KindStreakMilestone = "streak_milestone" // kids
)

// What a notification refers to.
const (
	RefInstance    = "instance"
	RefPointsEntry = "points_entry"
)

// InboxData is what a notification's text is rendered from. Which fields are set depends on the kind.
type InboxData struct {
	ChoreName  string `json:"choreName,omitempty" doc:"The chore's name when the notification was written (at delivery), not its current name"`
	ReasonCode string `json:"reasonCode,omitempty" doc:"chore_rejected: the quick-pick reason (not_done, not_finished, needs_redo or evidence_unclear)"`
	ReasonText string `json:"reasonText,omitempty" doc:"chore_rejected: the parent's feedback"`
	Auto       bool   `json:"auto,omitempty" doc:"chore_approved: the system approved it after the review timeout"`
	Days       int    `json:"days,omitempty" doc:"streak_milestone: the streak length reached"`
	Points     *int64 `json:"points,omitempty" doc:"streak_milestone: the bonus points; present even when 0, absent for other kinds"`
}

// InboxItem is one notification.
type InboxItem struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind" enum:"chore_submitted,chore_approved,chore_rejected,chore_missed,chore_excused,streak_milestone"`
	KidID     int64      `json:"kidId,omitempty" doc:"The kid it is about"`
	RefKind   string     `json:"refKind" enum:"instance,points_entry" doc:"What refId is: a chore instance or a points ledger entry"`
	RefID     int64      `json:"refId"`
	Data      InboxData  `json:"data"`
	CreatedAt time.Time  `json:"createdAt" doc:"When the event happened"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
}

// InboxPage is a page of notifications, newest delivered first (id descending, not event time).
type InboxPage struct {
	Items       []InboxItem `json:"items" nullable:"false"`
	NextCursor  string      `json:"nextCursor,omitempty" doc:"Pass as cursor to fetch the next page. Absent on the last page."`
	UnreadCount int64       `json:"unreadCount" doc:"All the caller's unread notifications, not only this page's"`
}

// Service writes and reads the inbox.
type Service struct {
	db       *db.DB
	clock    clock.Clock
	handlers map[string]events.Handler // by event type
}

// New returns a Service. d may be nil when only the API spec is built.
func New(d *db.DB, c clock.Clock) *Service {
	s := &Service{db: d, clock: c}
	s.handlers = map[string]events.Handler{
		instances.Completed{}.EventType():     s.completed,
		instances.Approved{}.EventType():      s.approved,
		instances.Rejected{}.EventType():      s.rejected,
		instances.Excused{}.EventType():       s.excused,
		dayclose.InstanceMissed{}.EventType(): s.missed,
		ledger.EntryPosted{}.EventType():      s.entryPosted,
	}
	return s
}

// Register subscribes the service to the events that notify.
func (s *Service) Register(bus *events.Bus) {
	for typ, h := range s.handlers {
		bus.Subscribe(typ, "inbox."+typ, h, jobs.KindOptions{})
	}
}

// Handle writes the notifications for one delivered event; other event types are ignored.
// Delivering the same event again writes nothing.
func (s *Service) Handle(ctx context.Context, env events.Envelope) error {
	if h, ok := s.handlers[env.Type]; ok {
		return h(ctx, env)
	}
	return nil
}

func (s *Service) completed(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Completed](env)
	if err != nil || ev.To != horizon.StatusSubmitted { // approved at once: nobody needs to review it
		return err
	}
	data, ok, err := s.choreData(ctx, ev.InstanceID)
	if err != nil || !ok {
		return err
	}
	// The kid who did it is the subject; a parent who completed it for them needs no notice.
	return s.toParents(ctx, env, KindChoreSubmitted, ev.CompletedBy, ev.By, RefInstance, ev.InstanceID, data)
}

func (s *Service) approved(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Approved](env)
	if err != nil {
		return err
	}
	return s.instanceToKids(ctx, env, KindChoreApproved, ev.InstanceID, ev.KidIDs, func(d *InboxData) { d.Auto = ev.By == 0 })
}

func (s *Service) rejected(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Rejected](env)
	if err != nil {
		return err
	}
	return s.instanceToKids(ctx, env, KindChoreRejected, ev.InstanceID, ev.KidIDs, func(d *InboxData) {
		d.ReasonCode, d.ReasonText = ev.ReasonCode, ev.ReasonText
	})
}

func (s *Service) excused(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Excused](env)
	if err != nil {
		return err
	}
	return s.instanceToKids(ctx, env, KindChoreExcused, ev.InstanceID, ev.KidIDs, nil)
}

func (s *Service) missed(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[dayclose.InstanceMissed](env)
	if err != nil {
		return err
	}
	return s.instanceToKids(ctx, env, KindChoreMissed, ev.InstanceID, ev.KidIDs, nil)
}

// entryPosted notifies a kid of a streak bonus. Other entries (chore credits, adjustments,
// reversals) don't notify yet.
func (s *Service) entryPosted(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[ledger.EntryPosted](env)
	if err != nil || ev.Kind != ledger.KindStreakBonus {
		return err
	}
	entry, err := s.db.GetPointsEntry(ctx, ev.EntryID)
	if err != nil {
		return fmt.Errorf("load points entry %d: %w", ev.EntryID, err)
	}
	data := InboxData{Days: streaks.MilestoneDays(entry.SourceID.Int64), Points: &ev.Amount}
	return s.toKid(ctx, env, KindStreakMilestone, ev.KidID, RefPointsEntry, ev.EntryID, data)
}

// choreData names the instance's chore. ok is false when the instance is gone, which leaves
// nothing to tell anyone about.
func (s *Service) choreData(ctx context.Context, instanceID int64) (InboxData, bool, error) {
	name, err := s.db.GetInstanceChoreName(ctx, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return InboxData{}, false, nil
	} else if err != nil {
		return InboxData{}, false, fmt.Errorf("load instance %d: %w", instanceID, err)
	}
	return InboxData{ChoreName: name}, true, nil
}

// instanceToKids notifies each of kidIDs about an instance; fill adds the kind's details.
func (s *Service) instanceToKids(ctx context.Context, env events.Envelope, kind string, instanceID int64, kidIDs []int64, fill func(*InboxData)) error {
	data, ok, err := s.choreData(ctx, instanceID)
	if err != nil || !ok {
		return err
	}
	if fill != nil {
		fill(&data)
	}
	for _, kid := range kidIDs {
		if err := s.toKid(ctx, env, kind, kid, RefInstance, instanceID, data); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) toKid(ctx context.Context, env events.Envelope, kind string, kid int64, refKind string, refID int64, data InboxData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode notification data: %w", err)
	}
	if _, err := s.db.InsertKidNotification(ctx, sqlc.InsertKidNotificationParams{
		Kind: kind, RefKind: refKind, RefID: refID, EventID: env.ID, Data: string(raw),
		CreatedAt: db.Timestamp(env.OccurredAt), Kid: kid,
	}); err != nil {
		return fmt.Errorf("notify kid %d (%s): %w", kid, kind, err)
	}
	return nil
}

// toParents notifies every parent except the user who acted (0 = nobody).
func (s *Service) toParents(ctx context.Context, env events.Envelope, kind string, kid, except int64, refKind string, refID int64, data InboxData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode notification data: %w", err)
	}
	if _, err := s.db.InsertParentNotifications(ctx, sqlc.InsertParentNotificationsParams{
		Kind: kind, Kid: kid, RefKind: refKind, RefID: refID, EventID: env.ID, Data: string(raw),
		CreatedAt: db.Timestamp(env.OccurredAt), Except: except,
	}); err != nil {
		return fmt.Errorf("notify parents (%s): %w", kind, err)
	}
	return nil
}

// List returns one page of recipient's notifications, newest delivered first (id descending), with their unread count.
func (s *Service) List(ctx context.Context, recipient int64, p paging.Params) (InboxPage, error) {
	before, first, err := paging.Decode[int64](p.Cursor)
	if err != nil {
		return InboxPage{}, err
	}
	if first {
		before = math.MaxInt64
	}
	size := p.Size()
	rows, err := s.db.ListNotifications(ctx, sqlc.ListNotificationsParams{RecipientID: recipient, ID: before, Limit: int64(size) + 1})
	if err != nil {
		return InboxPage{}, fmt.Errorf("list notifications: %w", err)
	}
	items := make([]InboxItem, 0, len(rows))
	for _, r := range rows {
		item, err := toItem(r)
		if err != nil {
			return InboxPage{}, err
		}
		items = append(items, item)
	}
	page, err := paging.Build(items, size, func(i InboxItem) int64 { return i.ID })
	if err != nil {
		return InboxPage{}, err
	}
	unread, err := s.db.CountUnreadNotifications(ctx, recipient)
	if err != nil {
		return InboxPage{}, fmt.Errorf("count unread notifications: %w", err)
	}
	return InboxPage{Items: page.Items, NextCursor: page.NextCursor, UnreadCount: unread}, nil
}

func toItem(r sqlc.Notification) (InboxItem, error) {
	created, err := db.ParseTimestamp(r.CreatedAt)
	if err != nil {
		return InboxItem{}, fmt.Errorf("notification %d: %w", r.ID, err)
	}
	item := InboxItem{ID: r.ID, Kind: r.Kind, KidID: r.KidID.Int64, RefKind: r.RefKind, RefID: r.RefID, CreatedAt: created}
	if err := json.Unmarshal([]byte(r.Data), &item.Data); err != nil {
		return InboxItem{}, fmt.Errorf("notification %d data: %w", r.ID, err)
	}
	if r.ReadAt.Valid {
		read, err := db.ParseTimestamp(r.ReadAt.String)
		if err != nil {
			return InboxItem{}, fmt.Errorf("notification %d: %w", r.ID, err)
		}
		item.ReadAt = &read
	}
	return item, nil
}

// MarkRead marks one of recipient's notifications read (keeping the first read time). Someone
// else's notification is as missing as one that doesn't exist.
func (s *Service) MarkRead(ctx context.Context, recipient, id int64) error {
	n, err := s.db.MarkNotificationRead(ctx, sqlc.MarkNotificationReadParams{
		Now: nullTime(s.clock.Now()), ID: id, Recipient: recipient,
	})
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	if n == 0 {
		return apierr.New(http.StatusNotFound, apierr.CodeNotificationNotFound, "no such notification")
	}
	return nil
}

// MarkAllRead marks all of recipient's unread notifications read.
func (s *Service) MarkAllRead(ctx context.Context, recipient int64) error {
	if _, err := s.db.MarkAllNotificationsRead(ctx, sqlc.MarkAllNotificationsReadParams{
		ReadAt: nullTime(s.clock.Now()), RecipientID: recipient,
	}); err != nil {
		return fmt.Errorf("mark notifications read: %w", err)
	}
	return nil
}

// Purge deletes notifications from events before cutoff, read or not. Called by daily maintenance.
func (s *Service) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.db.DeleteNotificationsBefore(ctx, db.Timestamp(cutoff))
}

func nullTime(t time.Time) sql.NullString {
	return sql.NullString{String: db.Timestamp(t), Valid: true}
}
