// Package audit is the household's audit log (docs/features/household.md, "Audit log"): who did
// what, for the owner. Rows are written two ways, and each audited action uses exactly one:
//
//   - from a domain event, when the event identifies the action on its own (approvals, rejections,
//     un-approvals, excuses, manual adjustments): package audit/recorder subscribes and calls
//     RecordEvent. Delivery is at least once; a redelivered event writes nothing.
//   - inside the action's own transaction otherwise (settings, invites, roles, removals, password
//     and 2FA resets, pairing, revocation, archive, manual reversals): the service calls Record
//     with the transaction's queries, so the row commits with the change or not at all.
//
// Backups and restores (internal/backup) also call Record: a backup after its snapshot, so the row
// is not in the archive it describes, and a restore into the restored database after the swap.
//
// Details are typed (AuditDetails), so tokens, password hashes, TOTP secrets and PINs have nowhere
// to go. Rows store no display text beyond the target's name at the time (summary); clients
// render the sentence. Rows are purged after Keep by daily maintenance.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/paging"
)

// Keep is how long audit rows are kept before maintenance purges them.
const Keep = 365 * 24 * time.Hour

// Actor kinds.
const (
	ActorParent = "parent" // a signed-in parent; Actor.ID is their user id
	ActorSystem = "system" // the server itself, e.g. an auto-approval
	ActorCLI    = "cli"    // `chorecast admin …` on the server host
)

// ActorKinds lists the actor kinds, for validating filters.
var ActorKinds = []string{ActorParent, ActorSystem, ActorCLI}

// Actions. Underscored, not dotted: clients look them up as translation keys.
const (
	ActionSettingsUpdated     = "settings_updated"
	ActionChoreApproved       = "chore_approved"
	ActionChoreRejected       = "chore_rejected"
	ActionChoreUnapproved     = "chore_unapproved"
	ActionChoreExcused        = "chore_excused"
	ActionPointsAdjusted      = "points_adjusted"
	ActionPointsReversed      = "points_reversed"
	ActionInviteCreated       = "invite_created"
	ActionInviteRevoked       = "invite_revoked"
	ActionInviteAccepted      = "invite_accepted"
	ActionParentRoleChanged   = "parent_role_changed"
	ActionParentRemoved       = "parent_removed"
	ActionParentPasswordReset = "parent_password_reset"
	ActionParentTOTPDisabled  = "parent_totp_disabled"
	ActionDevicePaired        = "device_paired"
	ActionDeviceRevoked       = "device_revoked"
	ActionKidArchived         = "kid_archived"
	ActionKidUnarchived       = "kid_unarchived"
	ActionBackupCreated       = "backup_created"
	ActionRestoreCompleted    = "restore_completed"
)

// Actions lists every action, for validating filters. Keep in step with AuditEntry.Action's enum.
var Actions = []string{
	ActionSettingsUpdated, ActionChoreApproved, ActionChoreRejected, ActionChoreUnapproved, ActionChoreExcused,
	ActionPointsAdjusted, ActionPointsReversed, ActionInviteCreated, ActionInviteRevoked, ActionInviteAccepted,
	ActionParentRoleChanged, ActionParentRemoved, ActionParentPasswordReset, ActionParentTOTPDisabled,
	ActionDevicePaired, ActionDeviceRevoked, ActionKidArchived, ActionKidUnarchived, ActionBackupCreated,
	ActionRestoreCompleted,
}

// What an entry is about.
const (
	TargetHousehold   = "household"
	TargetInstance    = "instance"
	TargetPointsEntry = "points_entry"
	TargetInvite      = "invite"
	TargetParent      = "parent"
	TargetDevice      = "device"
	TargetKid         = "kid"
	TargetBackup      = "backup" // a backup archive; the entry's summary is its file name
)

// Actor is who did something.
type Actor struct {
	Kind string
	ID   int64 // the parent; 0 for system and cli
}

var (
	System = Actor{Kind: ActorSystem}
	CLI    = Actor{Kind: ActorCLI}
)

// Parent is a parent acting.
func Parent(id int64) Actor { return Actor{Kind: ActorParent, ID: id} }

// UserOrSystem is the parent with id, or the system for 0 (the domain's "by" convention).
func UserOrSystem(id int64) Actor {
	if id == 0 {
		return System
	}
	return Parent(id)
}

// AuditDetails are an entry's details. Which fields are set depends on the action. Never add a
// field for a credential (token, password, hash, TOTP secret, PIN, pairing code).
type AuditDetails struct {
	Changes    []AuditChange `json:"changes,omitempty" doc:"settings_updated: each setting that changed"`
	KidID      int64         `json:"kidId,omitempty" doc:"points_*: the kid; device_paired: the kid it belongs to (absent = a shared device)"`
	KidIDs     []int64       `json:"kidIds,omitempty" doc:"chore_*: the assignee, or a shared chore's members"`
	Amount     *int64        `json:"amount,omitempty" doc:"points_*: the signed points posted"`
	Note       string        `json:"note,omitempty" doc:"points_*: the note"`
	ReversesID int64         `json:"reversesId,omitempty" doc:"points_reversed: the entry undone"`
	ReasonCode string        `json:"reasonCode,omitempty" doc:"chore_rejected: the quick-pick reason"`
	ReasonText string        `json:"reasonText,omitempty" doc:"chore_rejected: the parent's feedback"`
	Role       string        `json:"role,omitempty" enum:"parent,owner" doc:"invite_*: the invite's role; parent_role_changed: the new role"`
	Bytes      int64         `json:"bytes,omitempty" doc:"backup_created: the archive's size"`
	MediaFiles *int64        `json:"mediaFiles,omitempty" doc:"backup_created: media files archived (absent when a retry found the archive already written)"`
	AppVersion string        `json:"appVersion,omitempty" doc:"restore_completed: the Chorecast version that took the backup"`
	SchemaVer  int64         `json:"schemaVersion,omitempty" doc:"restore_completed: the backup's schema version"`
	Forced     *bool         `json:"forced,omitempty" doc:"restore_completed: whether --force replaced existing data"`
}

// AuditChange is one changed setting. Values are as the settings API shows them; lists and other
// structured values are JSON.
type AuditChange struct {
	Field string `json:"field" doc:"The setting's name in the household settings API, e.g. dayEnd"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// Entry is a row to record.
type Entry struct {
	Actor      Actor
	Action     string
	TargetKind string
	TargetID   int64 // 0 = none (household settings)
	Summary    string
	Details    AuditDetails
}

// Record writes e, done at at. Pass the action's transaction queries so the row commits with it.
func Record(ctx context.Context, q *sqlc.Queries, at time.Time, e Entry) error {
	return insert(ctx, q, at, 0, e)
}

// RecordEvent writes e for a delivered event, dated when the event happened. An event has at most
// one row: recording the same event again, even with another entry, writes nothing.
func RecordEvent(ctx context.Context, q *sqlc.Queries, env events.Envelope, e Entry) error {
	return insert(ctx, q, env.OccurredAt, env.ID, e)
}

func insert(ctx context.Context, q *sqlc.Queries, at time.Time, eventID int64, e Entry) error {
	raw, err := json.Marshal(e.Details)
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	if _, err := q.InsertAuditEntry(ctx, sqlc.InsertAuditEntryParams{
		ActorKind: e.Actor.Kind, ActorID: nullInt(e.Actor.ID), Action: e.Action,
		TargetKind: e.TargetKind, TargetID: nullInt(e.TargetID), Summary: e.Summary, Data: string(raw),
		EventID: nullInt(eventID), CreatedAt: db.Timestamp(at),
	}); err != nil {
		return fmt.Errorf("audit %s: %w", e.Action, err)
	}
	return nil
}

// AuditEntry is one audit log row as the API returns it.
type AuditEntry struct {
	ID         int64        `json:"id"`
	CreatedAt  time.Time    `json:"createdAt" doc:"When it happened"`
	ActorKind  string       `json:"actorKind" enum:"parent,system,cli" doc:"parent = a signed-in parent; system = the server (e.g. an auto-approval); cli = an admin command on the server host"`
	ActorID    int64        `json:"actorId,omitempty" doc:"The parent who acted"`
	ActorName  string       `json:"actorName,omitempty" doc:"The parent's name now"`
	Action     string       `json:"action" enum:"settings_updated,chore_approved,chore_rejected,chore_unapproved,chore_excused,points_adjusted,points_reversed,invite_created,invite_revoked,invite_accepted,parent_role_changed,parent_removed,parent_password_reset,parent_totp_disabled,device_paired,device_revoked,kid_archived,kid_unarchived,backup_created,restore_completed"`
	TargetKind string       `json:"targetKind" enum:"household,instance,points_entry,invite,parent,device,kid,backup" doc:"What targetId is"`
	TargetID   int64        `json:"targetId,omitempty" doc:"Absent for household settings"`
	Summary    string       `json:"summary,omitempty" doc:"The target's name when it happened (chore, kid, parent or device)"`
	Details    AuditDetails `json:"details"`
}

// Filter narrows List. Zero fields match everything.
type Filter struct {
	ActorKind string
	ActorID   int64
	Action    string
	Since     time.Time // inclusive
	Until     time.Time // exclusive
}

// Service reads and purges the audit log.
type Service struct{ db *db.DB }

// New returns a Service. d may be nil when only the API spec is built.
func New(d *db.DB) *Service { return &Service{db: d} }

// List returns one page of entries matching f, newest recorded first (id descending).
func (s *Service) List(ctx context.Context, f Filter, p paging.Params) (paging.Page[AuditEntry], error) {
	before, first, err := paging.Decode[int64](p.Cursor)
	if err != nil {
		return paging.Page[AuditEntry]{}, err
	}
	if first {
		before = math.MaxInt64
	}
	since, until := "", "9999"
	if !f.Since.IsZero() {
		since = db.Timestamp(f.Since)
	}
	if !f.Until.IsZero() {
		until = db.Timestamp(f.Until)
	}
	size := p.Size()
	rows, err := s.db.ListAuditEntries(ctx, sqlc.ListAuditEntriesParams{
		Before: before, ActorKind: f.ActorKind, ActorID: f.ActorID, Action: f.Action,
		Since: since, Until: until, Lim: int64(size) + 1,
	})
	if err != nil {
		return paging.Page[AuditEntry]{}, fmt.Errorf("list audit log: %w", err)
	}
	items := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		created, err := db.ParseTimestamp(r.CreatedAt)
		if err != nil {
			return paging.Page[AuditEntry]{}, fmt.Errorf("audit entry %d: %w", r.ID, err)
		}
		item := AuditEntry{
			ID: r.ID, CreatedAt: created, ActorKind: r.ActorKind, ActorID: r.ActorID.Int64, ActorName: r.ActorName,
			Action: r.Action, TargetKind: r.TargetKind, TargetID: r.TargetID.Int64, Summary: r.Summary,
		}
		if err := json.Unmarshal([]byte(r.Data), &item.Details); err != nil {
			return paging.Page[AuditEntry]{}, fmt.Errorf("audit entry %d details: %w", r.ID, err)
		}
		items = append(items, item)
	}
	return paging.Build(items, size, func(e AuditEntry) int64 { return e.ID })
}

// Purge deletes entries from before cutoff. Called by daily maintenance.
func (s *Service) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.db.DeleteAuditEntriesBefore(ctx, db.Timestamp(cutoff))
}

func nullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: v != 0} }
