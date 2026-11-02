package media

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// expireBatch is how many media rows Expire handles per query.
const expireBatch = 100

// ExpireResult is what one retention run did.
type ExpireResult struct {
	Expired int // items whose files were deleted and that are now expired
	Failed  int // claimed items whose files couldn't be deleted; the next run retries them
}

// Expire is the retention job (docs/features/approvals-evidence.md, Media pipeline). It takes
// every ready, unkept media item that is past its expires_at (evidence whose instance resolved
// more than media_retention_days ago, pending uploads never attached within PendingTTL), or that
// is evidence whose record is gone (its instance was deleted with its kid). The rows and their
// media_hashes entries stay, so reuse is still caught, except for abandoned pending uploads,
// which were never evidence: their hash is dropped so the kid may upload that photo again.
//
// It runs in two steps so a reopened instance can't end up with a ready row and no files:
//
//  1. Claim: each item becomes status deleting in a transaction that re-checks it still
//     qualifies. Resolve, Reopen and attaching touch only ready media, so a claimed item stays
//     claimed, and reads already show it as expired.
//  2. Delete: the files of every claimed item, this run's and any an earlier run left (a crash,
//     or a file that couldn't be deleted), are deleted and the item becomes status expired. A
//     file that can't be deleted is logged and counted, and the run goes on with the others.
//
// Re-running finds nothing new, and deleting a missing file is no error, so the job is idempotent.
func Expire(ctx context.Context, d *db.DB, clk clock.Clock, store Storage, log *slog.Logger) (ExpireResult, error) {
	now := clk.Now()
	for {
		batch, err := d.ListExpiredMedia(ctx, sqlc.ListExpiredMediaParams{
			Now: sql.NullString{String: db.Timestamp(now), Valid: true}, MaxRows: expireBatch,
		})
		if err != nil {
			return ExpireResult{}, fmt.Errorf("list expired media: %w", err)
		}
		for _, m := range batch {
			if err := claimInTx(ctx, d, m, now); err != nil {
				return ExpireResult{}, err
			}
		}
		// A listed item claim skipped no longer qualifies, so it isn't listed again.
		if len(batch) < expireBatch {
			break
		}
	}

	var res ExpireResult
	after := int64(0)
	for {
		batch, err := d.ListDeletingMedia(ctx, sqlc.ListDeletingMediaParams{AfterID: after, MaxRows: expireBatch})
		if err != nil {
			return res, fmt.Errorf("list claimed media: %w", err)
		}
		for _, m := range batch {
			after = m.ID
			if err := deleteFiles(ctx, store, m); err != nil {
				res.Failed++
				log.Warn("media retention: can't delete the files; the next run retries", "media", m.ID, "err", err)
				continue
			}
			if err := d.MarkMediaExpired(ctx, m.ID); err != nil {
				return res, fmt.Errorf("mark media %d expired: %w", m.ID, err)
			}
			res.Expired++
		}
		if len(batch) < expireBatch {
			return res, nil
		}
	}
}

func claimInTx(ctx context.Context, d *db.DB, m sqlc.Medium, now time.Time) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := claim(ctx, d.WithTx(tx), m, now); err != nil {
		return err
	}
	return tx.Commit()
}

// claim marks m deleting if it still qualifies for retention, dropping an abandoned pending
// upload's hash; run it in a transaction. It reports whether m was claimed.
func claim(ctx context.Context, q *sqlc.Queries, m sqlc.Medium, now time.Time) (bool, error) {
	n, err := q.ClaimExpiredMedia(ctx, sqlc.ClaimExpiredMediaParams{
		ID: m.ID, Now: sql.NullString{String: db.Timestamp(now), Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("claim media %d: %w", m.ID, err)
	}
	if n == 0 {
		return false, nil
	}
	if m.OwnerKind == OwnerPending {
		if err := q.DeleteMediaHashOf(ctx, sql.NullInt64{Int64: m.ID, Valid: true}); err != nil {
			return false, fmt.Errorf("drop media %d hash: %w", m.ID, err)
		}
	}
	return true, nil
}

func deleteFiles(ctx context.Context, store Storage, m sqlc.Medium) error {
	keys := []string{m.StorageKey}
	if m.ThumbKey.Valid {
		keys = append(keys, m.ThumbKey.String)
	}
	for _, k := range keys {
		if err := store.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
