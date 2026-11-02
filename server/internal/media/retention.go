package media

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// expireBatch is how many media rows Expire handles per query.
const expireBatch = 100

// Expire is the retention job (docs/features/approvals-evidence.md, Media pipeline). It deletes
// the files of every ready media item whose expires_at has passed and that isn't kept: evidence
// whose instance resolved more than media_retention_days ago, and pending uploads never attached
// within PendingTTL. Evidence whose record is gone (its instance was deleted with its kid) goes
// too. Each row then becomes status expired; the row and its media_hashes entry stay, so reuse
// is still caught, except for abandoned pending uploads, which were never evidence: their hash
// is dropped so the kid may upload that photo again. It returns how many items expired.
//
// Files are deleted before the row is marked, so a run that stops halfway is finished by the
// next one: deleting a missing file is no error. An item whose expiry was cleared meanwhile (its
// instance reopened) isn't marked; at worst it has lost files that were due for deletion.
func Expire(ctx context.Context, d *db.DB, clk clock.Clock, store Storage) (int, error) {
	now := sql.NullString{String: db.Timestamp(clk.Now()), Valid: true}
	total := 0
	for {
		batch, err := d.ListExpiredMedia(ctx, sqlc.ListExpiredMediaParams{Now: now, MaxRows: expireBatch})
		if err != nil {
			return total, fmt.Errorf("list expired media: %w", err)
		}
		for _, m := range batch {
			n, err := expireOne(ctx, d.Queries, store, m, now)
			if err != nil {
				return total, err
			}
			total += int(n)
		}
		if len(batch) < expireBatch {
			return total, nil
		}
	}
}

// expireOne deletes m's files and marks it expired; it returns the rows marked (0 or 1).
func expireOne(ctx context.Context, q *sqlc.Queries, store Storage, m sqlc.Medium, now sql.NullString) (int64, error) {
	keys := []string{m.StorageKey}
	if m.ThumbKey.Valid {
		keys = append(keys, m.ThumbKey.String)
	}
	for _, k := range keys {
		if err := store.Delete(ctx, k); err != nil {
			return 0, fmt.Errorf("delete media %d file: %w", m.ID, err)
		}
	}
	if m.OwnerKind == OwnerPending {
		if err := q.DeleteMediaHashOf(ctx, sql.NullInt64{Int64: m.ID, Valid: true}); err != nil {
			return 0, fmt.Errorf("drop media %d hash: %w", m.ID, err)
		}
	}
	n, err := q.MarkMediaExpired(ctx, sqlc.MarkMediaExpiredParams{ID: m.ID, Now: now})
	if err != nil {
		return 0, fmt.Errorf("mark media %d expired: %w", m.ID, err)
	}
	return n, nil
}
