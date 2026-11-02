// Package evidence checks what comes with a chore completion (ticked checklist steps, a note,
// photos) against the chore's evidence rules, attaches it to the instance, sets its media's
// retention when the instance resolves, and reads it back for parents and the instance's kids.
// Spec: docs/features/approvals-evidence.md (Submission flow, Fresh-evidence rules, Retention).
//
// Every write runs on the caller's transaction (tx.Q): Attach in instances.Complete, Resolve and
// Reopen in the transitions and day close.
package evidence

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
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/media"
)

// Photo roles, for chores that ask for a before and an after photo.
const (
	RoleBefore = "before"
	RoleAfter  = "after"
)

// Limits on one submission. MaxPhotos is the spec's files-per-submission default, which is also
// the most photos a chore may ask for.
const (
	MaxPhotos          = 5
	MaxNoteLength      = 500
	MaxCaptureIDLength = 64
)

// ErrVideoUnsupported refuses completing a chore that asks for a video. Video evidence is Phase 3,
// so such a chore can't be completed yet; a parent can excuse it when it's missed.
var ErrVideoUnsupported = apierr.New(http.StatusUnprocessableEntity, apierr.CodeVideoUnsupported,
	"this chore asks for a video, which can't be submitted yet; ask a parent to change the chore")

// EvidencePhotoInput is one photo of a submission: a pending upload from POST /media/photos.
type EvidencePhotoInput struct {
	MediaID    int64     `json:"mediaId" minimum:"1" doc:"A pending photo from POST /media/photos"`
	Role       string    `json:"role,omitempty" enum:"before,after" doc:"Before or after the chore; the chore may require one of each"`
	CapturedAt time.Time `json:"capturedAt,omitzero" doc:"Device time at the shutter; required for a kid's photo"`
	CaptureID  string    `json:"captureId,omitempty" maxLength:"64" doc:"The app's id for the capture; required for a kid's photo"`
}

// Submission is the evidence that comes with a completion.
type Submission struct {
	Checklist []int // indexes of the ticked steps
	Note      string
	Photos    []EvidencePhotoInput
}

// Target is the completion a submission belongs to.
type Target struct {
	InstanceID int64
	KidID      int64 // the kid who did it
	By         int64 // the acting user: the kid, or a parent completing for them
	// OpensAt is when the instance became available (dayclose.OpensAt): kids' photos must be
	// captured after it.
	OpensAt time.Time
}

// Attach checks sub against chore's evidence rules and records it for t in the caller's
// transaction: a new evidence row (the instance's next attempt, so earlier submissions stay) with
// its photos moved from pending to that row. A failed rule is 422 validation_failed with the
// body field's location; a chore asking for a video is ErrVideoUnsupported.
//
// A photo must be a pending upload by the kid, or by the parent completing for them, that wasn't
// attached before. A kid's photo needs its capture time and id, and must be captured at or after
// t.OpensAt; a parent's photo is exempt (fresh-evidence rule 5).
func Attach(ctx context.Context, q *sqlc.Queries, clk clock.Clock, chore sqlc.Chore, t Target, sub Submission) error {
	if chore.EvidenceVideo == 1 {
		return ErrVideoUnsupported
	}
	sub.Note = strings.TrimSpace(sub.Note)
	checklist := slices.Clone(sub.Checklist)
	slices.Sort(checklist)
	checklist = slices.Compact(checklist)
	now := clk.Now()

	details, err := validate(ctx, q, now, chore, t, sub, checklist)
	if err != nil {
		return err
	}
	if len(details) > 0 {
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}

	n, err := q.CountChoreInstanceEvidence(ctx, t.InstanceID)
	if err != nil {
		return fmt.Errorf("count evidence: %w", err)
	}
	ticks, err := json.Marshal(checklist)
	if err != nil {
		return err
	}
	id, err := q.InsertChoreInstanceEvidence(ctx, sqlc.InsertChoreInstanceEvidenceParams{
		InstanceID: t.InstanceID, Attempt: n + 1,
		KidID:       sql.NullInt64{Int64: t.KidID, Valid: true},
		SubmittedBy: sql.NullInt64{Int64: t.By, Valid: t.By != 0},
		Checklist:   string(ticks), Note: sub.Note, CreatedAt: db.Timestamp(now),
	})
	if err != nil {
		return fmt.Errorf("record evidence: %w", err)
	}
	for i, p := range sub.Photos {
		moved, err := q.AttachEvidenceMedia(ctx, sqlc.AttachEvidenceMediaParams{
			EvidenceID: sql.NullInt64{Int64: id, Valid: true}, ID: p.MediaID,
			Now: sql.NullString{String: db.Timestamp(now), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("attach media %d: %w", p.MediaID, err)
		}
		if moved == 0 { // validate saw it pending; the single connection makes this unexpected
			return photoProblem(i, p, "this photo can't be used any more; take a new one")
		}
		captured := sql.NullString{}
		if !p.CapturedAt.IsZero() {
			captured = sql.NullString{String: db.Timestamp(p.CapturedAt), Valid: true}
		}
		if err := q.InsertChoreInstanceEvidencePhoto(ctx, sqlc.InsertChoreInstanceEvidencePhotoParams{
			MediaID: p.MediaID, EvidenceID: id, Position: int64(i), Role: p.Role,
			CapturedAt: captured, CaptureID: p.CaptureID,
		}); err != nil {
			return fmt.Errorf("record evidence photo %d: %w", p.MediaID, err)
		}
	}
	return nil
}

func photoProblem(i int, p EvidencePhotoInput, msg string) error {
	return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed",
		&huma.ErrorDetail{Location: fmt.Sprintf("body.photos[%d].mediaId", i), Message: msg, Value: p.MediaID})
}

// validate returns a problem detail for every rule sub breaks. checklist is sub.Checklist
// sorted and without repeats.
func validate(ctx context.Context, q *sqlc.Queries, now time.Time, chore sqlc.Chore, t Target, sub Submission, checklist []int) ([]error, error) {
	var details []error
	bad := func(loc, msg string, v any) {
		details = append(details, &huma.ErrorDetail{Location: "body." + loc, Message: msg, Value: v})
	}

	var steps []string
	if err := json.Unmarshal([]byte(chore.Steps), &steps); err != nil {
		return nil, fmt.Errorf("chore %d steps: %w", chore.ID, err)
	}
	for i, s := range sub.Checklist {
		if s < 0 || s >= len(steps) {
			bad(fmt.Sprintf("checklist[%d]", i), "no such step", s)
		}
	}
	if chore.EvidenceChecklist == 1 && len(checklist) < len(steps) {
		bad("checklist", "tick every step", sub.Checklist)
	}

	if utf8.RuneCountInString(sub.Note) > MaxNoteLength {
		bad("note", fmt.Sprintf("at most %d characters", MaxNoteLength), sub.Note)
	}
	if chore.EvidenceNote == 1 && sub.Note == "" {
		bad("note", "write a note", sub.Note)
	}

	switch n := len(sub.Photos); {
	case n > MaxPhotos:
		bad("photos", fmt.Sprintf("at most %d photos", MaxPhotos), n)
	case n < int(chore.EvidencePhotos):
		bad("photos", fmt.Sprintf("take at least %d photos", chore.EvidencePhotos), n)
	}
	roles := map[string]bool{}
	seen := map[int64]bool{}
	for i, p := range sub.Photos {
		at := fmt.Sprintf("photos[%d].", i)
		switch p.Role {
		case "", RoleBefore, RoleAfter:
			roles[p.Role] = true
		default:
			bad(at+"role", "before or after", p.Role)
		}
		if seen[p.MediaID] {
			bad(at+"mediaId", "this photo is listed twice", p.MediaID)
			continue
		}
		seen[p.MediaID] = true
		msg, err := checkPhoto(ctx, q, now, t, p)
		if err != nil {
			return nil, err
		}
		if msg.text != "" {
			bad(at+msg.field, msg.text, msg.value)
		}
	}
	if chore.EvidenceBeforeAfter == 1 && (!roles[RoleBefore] || !roles[RoleAfter]) {
		bad("photos", "include a before and an after photo", len(sub.Photos))
	}
	return details, nil
}

type photoMsg struct {
	field, text string
	value       any
}

// checkPhoto checks that p's media may be attached for t, and a kid's photo's capture.
func checkPhoto(ctx context.Context, q *sqlc.Queries, now time.Time, t Target, p EvidencePhotoInput) (photoMsg, error) {
	m, err := q.GetMedia(ctx, p.MediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return photoMsg{"mediaId", "no such photo", p.MediaID}, nil
	}
	if err != nil {
		return photoMsg{}, fmt.Errorf("load media %d: %w", p.MediaID, err)
	}
	byKid := m.UploadedBy.Valid && m.UploadedBy.Int64 == t.KidID
	byParent := false
	if !byKid && m.UploadedBy.Valid && m.UploadedBy.Int64 == t.By {
		u, err := q.GetUser(ctx, t.By)
		if err != nil {
			return photoMsg{}, fmt.Errorf("load user %d: %w", t.By, err)
		}
		byParent = u.Role == "parent"
	}
	switch {
	case !byKid && !byParent: // someone else's: answered as if it didn't exist
		return photoMsg{"mediaId", "no such photo", p.MediaID}, nil
	case m.OwnerKind != media.OwnerPending:
		return photoMsg{"mediaId", "this photo was already used as evidence; take a new one", p.MediaID}, nil
	case !media.Attachable(m, now):
		return photoMsg{"mediaId", "this photo has expired; upload it again", p.MediaID}, nil
	case byParent:
		return photoMsg{}, nil // parents' photos are exempt from the capture rules
	case p.CapturedAt.IsZero():
		return photoMsg{"capturedAt", "say when the photo was taken", nil}, nil
	case p.CaptureID == "":
		return photoMsg{"captureId", "the photo's capture id is missing", p.CaptureID}, nil
	case p.CapturedAt.Before(t.OpensAt):
		return photoMsg{"capturedAt", "this photo was taken before the chore started; take a new one", p.CapturedAt}, nil
	}
	return photoMsg{}, nil
}

// Resolve starts the retention clock of an instance's evidence media when the instance resolves
// at at (approved, missed or excused): it expires media_retention_days later, unless a parent
// keeps it. A later resolution (missed, then excused) restarts it.
func Resolve(ctx context.Context, q *sqlc.Queries, instanceID int64, at time.Time) error {
	h, err := q.GetHousehold(ctx)
	if err != nil {
		return fmt.Errorf("load household: %w", err)
	}
	expires := at.AddDate(0, 0, int(h.MediaRetentionDays))
	if err := q.SetChoreInstanceEvidenceExpiry(ctx, sqlc.SetChoreInstanceEvidenceExpiryParams{
		ExpiresAt: sql.NullString{String: db.Timestamp(expires), Valid: true}, InstanceID: instanceID,
	}); err != nil {
		return fmt.Errorf("set evidence expiry: %w", err)
	}
	return nil
}

// Reopen stops the retention clock of an instance that is open again (an approval reversed).
func Reopen(ctx context.Context, q *sqlc.Queries, instanceID int64) error {
	if err := q.ClearChoreInstanceEvidenceExpiry(ctx, instanceID); err != nil {
		return fmt.Errorf("clear evidence expiry: %w", err)
	}
	return nil
}

// InstanceEvidence is one submission's evidence as the API returns it.
type InstanceEvidence struct {
	Attempt     int64           `json:"attempt" doc:"Which submission of the instance this is; 1 = the first"`
	KidID       int64           `json:"kidId,omitempty" doc:"The kid who did it"`
	SubmittedBy int64           `json:"submittedBy,omitempty" doc:"Who submitted it: the kid, or a parent for them"`
	SubmittedAt time.Time       `json:"submittedAt"`
	Checklist   []int           `json:"checklist" nullable:"false" doc:"Indexes of the ticked steps"`
	Note        string          `json:"note,omitempty"`
	Photos      []EvidencePhoto `json:"photos" nullable:"false"`
}

// EvidencePhoto is a submitted photo: signed links to it, or Expired once retention deleted it.
type EvidencePhoto struct {
	MediaID      int64     `json:"mediaId"`
	Role         string    `json:"role,omitempty" enum:"before,after"`
	CapturedAt   time.Time `json:"capturedAt,omitzero" doc:"Device time at the shutter (kids' photos)"`
	CaptureID    string    `json:"captureId,omitempty"`
	Expired      bool      `json:"expired,omitempty" doc:"Retention deleted the file: there are no links"`
	URL          string    `json:"url,omitempty" format:"uri" doc:"Signed link, valid until urlExpiresAt"`
	ThumbURL     string    `json:"thumbUrl,omitempty" format:"uri" doc:"Signed link to the thumbnail, valid until urlExpiresAt"`
	URLExpiresAt time.Time `json:"urlExpiresAt,omitzero"`
	Width        int64     `json:"width,omitempty"`
	Height       int64     `json:"height,omitempty"`
}

// Signer makes the signed links to a media item (media.Service.URLs; the API makes them absolute).
type Signer func(ctx context.Context, m sqlc.Medium) (media.SignedURLs, error)

// CanRead reports whether p may read inst's evidence: parents, and the instance's kids (its
// assignee, or a shared instance's members). Callers check this before List.
func CanRead(p *auth.Principal, inst horizon.ChoreInstance) bool {
	switch p.Kind {
	case auth.KindParent:
		return true
	case auth.KindKid:
		return inst.KidID == p.UserID || slices.Contains(inst.GroupKidIDs, p.UserID)
	default:
		return false
	}
}

// List returns every submission's evidence for the instance, oldest first, with fresh links
// from sign. Photos whose files retention deleted are marked Expired and have no links.
func List(ctx context.Context, q *sqlc.Queries, instanceID int64, sign Signer) ([]InstanceEvidence, error) {
	rows, err := q.ListChoreInstanceEvidence(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	photoRows, err := q.ListChoreInstanceEvidencePhotos(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list evidence photos: %w", err)
	}
	photos := map[int64][]EvidencePhoto{}
	for _, r := range photoRows {
		p, err := toPhoto(ctx, r, sign)
		if err != nil {
			return nil, err
		}
		photos[r.ChoreInstanceEvidencePhoto.EvidenceID] = append(photos[r.ChoreInstanceEvidencePhoto.EvidenceID], p)
	}
	out := make([]InstanceEvidence, 0, len(rows))
	for _, r := range rows {
		at, err := db.ParseTimestamp(r.CreatedAt)
		if err != nil {
			return nil, err
		}
		ev := InstanceEvidence{
			Attempt: r.Attempt, KidID: r.KidID.Int64, SubmittedBy: r.SubmittedBy.Int64, SubmittedAt: at,
			Note: r.Note, Checklist: []int{}, Photos: photos[r.ID],
		}
		if err := json.Unmarshal([]byte(r.Checklist), &ev.Checklist); err != nil {
			return nil, fmt.Errorf("evidence %d checklist: %w", r.ID, err)
		}
		if ev.Photos == nil {
			ev.Photos = []EvidencePhoto{}
		}
		out = append(out, ev)
	}
	return out, nil
}

func toPhoto(ctx context.Context, r sqlc.ListChoreInstanceEvidencePhotosRow, sign Signer) (EvidencePhoto, error) {
	ph, m := r.ChoreInstanceEvidencePhoto, r.Medium
	out := EvidencePhoto{MediaID: m.ID, Role: ph.Role, CaptureID: ph.CaptureID}
	if ph.CapturedAt.Valid {
		at, err := db.ParseTimestamp(ph.CapturedAt.String)
		if err != nil {
			return EvidencePhoto{}, err
		}
		out.CapturedAt = at
	}
	if m.Status != media.StatusReady {
		out.Expired = true
		return out, nil
	}
	urls, err := sign(ctx, m)
	if err != nil {
		return EvidencePhoto{}, err
	}
	out.URL, out.ThumbURL, out.URLExpiresAt = urls.URL, urls.ThumbURL, urls.Expires
	out.Width, out.Height = m.Width, m.Height
	return out, nil
}
