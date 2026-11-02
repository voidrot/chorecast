-- name: ListDueOpenChoreInstances :many
-- Open instances due by the instant (a timestamp): one kid's (kid > 0) or the shared ones
-- (kid = 0), flexible or not, with what missing them costs.
SELECT i.id, i.kid_id, i.occurrence_date, i.flexible, c.penalty, c.shared_penalty
FROM chore_instances i
JOIN chores c ON c.id = i.chore_id
WHERE i.status = 'open' AND i.due_at <= sqlc.arg(due_by)
  AND COALESCE(i.kid_id, 0) = CAST(sqlc.arg(kid) AS INTEGER) AND i.flexible = sqlc.arg(flexible)
ORDER BY i.due_at, i.id;

-- name: MarkChoreInstanceMissed :execrows
-- 0 rows: the instance isn't open any more (someone else moved it), so leave it.
UPDATE chore_instances SET status = 'missed', updated_at = ? WHERE id = ? AND status = 'open';

-- name: ListChoreInstanceMembers :many
SELECT kid_id FROM chore_instance_members WHERE instance_id = ? ORDER BY kid_id;
