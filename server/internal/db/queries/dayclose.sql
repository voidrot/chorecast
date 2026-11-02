-- name: ListDueOpenChoreInstanceIDs :many
-- Open instances due by the instant (a timestamp): one kid's (kid > 0) or the shared ones
-- (kid = 0), flexible or not. Uses chore_instances_open_due.
SELECT id FROM chore_instances
WHERE status = 'open' AND due_at <= sqlc.arg(due_by)
  AND COALESCE(kid_id, 0) = CAST(sqlc.arg(kid) AS INTEGER) AND flexible = sqlc.arg(flexible)
ORDER BY due_at, id;

-- name: MarkChoreInstanceMissed :one
-- No row: the instance isn't open any more (someone else moved it), so leave it. The returned
-- columns are read in the same statement, so a reassignment can't slip in between.
UPDATE chore_instances SET status = 'missed', updated_at = ? WHERE id = ? AND status = 'open'
RETURNING kid_id, occurrence_date, flexible, chore_id;

-- name: GetChorePenalty :one
SELECT penalty, shared_penalty FROM chores WHERE id = ?;

-- name: ListChoreInstanceMembers :many
SELECT kid_id FROM chore_instance_members WHERE instance_id = ? ORDER BY kid_id;
