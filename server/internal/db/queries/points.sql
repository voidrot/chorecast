-- name: InsertPointsEntry :one
INSERT INTO points_entries (kid_id, kind, amount, requested_amount, local_date, source_kind, source_id,
    reverses_id, note, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPointsEntry :one
SELECT * FROM points_entries WHERE id = ?;

-- name: GetPointsEntryBySource :one
SELECT * FROM points_entries
WHERE kind = ? AND source_kind = ? AND source_id = ? AND kid_id = ?;

-- name: ListPointsKidsBySource :many
-- The kids with a kind entry for the source (the heads of their chains), by kid id.
SELECT kid_id FROM points_entries
WHERE kind = ? AND source_kind = ? AND source_id = ?
ORDER BY kid_id;

-- name: GetPointsReversalOf :one
SELECT * FROM points_entries WHERE reverses_id = ?;

-- name: KidPointsBalance :one
SELECT CAST(COALESCE(SUM(amount), 0) AS INTEGER) FROM points_entries WHERE kid_id = ?;

-- name: KidPointsBalanceThrough :one
SELECT CAST(COALESCE(SUM(amount), 0) AS INTEGER) FROM points_entries WHERE kid_id = ? AND id <= ?;

-- name: ListKidPointsEntries :many
-- Newest first, before the cursor id; reversed_by is the entry's reversal, if any.
SELECT sqlc.embed(p), r.id AS reversed_by
FROM points_entries p LEFT JOIN points_entries r ON r.reverses_id = p.id
WHERE p.kid_id = ? AND p.id < ?
ORDER BY p.id DESC LIMIT ?;
