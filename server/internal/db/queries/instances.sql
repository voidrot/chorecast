-- name: ListChoreInstancesFrom :many
-- Every instance dated on or after the day, with the chore's estimate for the fairness ledger.
SELECT i.*, c.estimated_minutes FROM chore_instances i
JOIN chores c ON c.id = i.chore_id
WHERE i.occurrence_date >= ?
ORDER BY i.chore_id, i.occurrence_date, i.slot, i.seq, i.kid_id;

-- name: ListChoreInstanceMembersFrom :many
SELECT m.instance_id, m.kid_id FROM chore_instance_members m
JOIN chore_instances i ON i.id = m.instance_id
WHERE i.occurrence_date >= ?
ORDER BY m.instance_id, m.kid_id;

-- name: LatestChoreRotationStateBefore :one
-- The rotation state after the chore's last instance dated before the day.
SELECT rotation_state FROM chore_instances
WHERE chore_id = ? AND rotation_state IS NOT NULL AND occurrence_date < ?
ORDER BY occurrence_date DESC, slot DESC, seq DESC
LIMIT 1;

-- name: InsertChoreInstance :one
INSERT INTO chore_instances (chore_id, kid_id, occurrence_date, slot, seq, due_at, flexible, auto_assigned,
    rotation_state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateChoreInstancePlan :exec
UPDATE chore_instances SET due_at = ?, flexible = ?, auto_assigned = ?, rotation_state = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteChoreInstance :exec
DELETE FROM chore_instances WHERE id = ?;

-- name: AddChoreInstanceMember :exec
INSERT INTO chore_instance_members (instance_id, kid_id) VALUES (?, ?);

-- name: DeleteChoreInstanceMembers :exec
DELETE FROM chore_instance_members WHERE instance_id = ?;

-- name: SetChoreRotationState :exec
UPDATE chores SET rotation_state = ? WHERE id = ?;

-- name: ClearChoreInstanceRotationStates :exec
-- A rotation that changed starts over: earlier instances' states belong to the old one.
UPDATE chore_instances SET rotation_state = NULL WHERE chore_id = ?;

-- name: ListChoreInstancesBetween :many
-- Instances on [from, to] for every kid (kid = 0) or one kid, including shared instances they're a member of.
SELECT i.*, c.name AS chore_name FROM chore_instances i
JOIN chores c ON c.id = i.chore_id
WHERE i.occurrence_date >= sqlc.arg(from_date) AND i.occurrence_date <= sqlc.arg(to_date)
  AND (CAST(sqlc.arg(kid) AS INTEGER) = 0 OR i.kid_id = CAST(sqlc.arg(kid) AS INTEGER)
       OR EXISTS (SELECT 1 FROM chore_instance_members m
                  WHERE m.instance_id = i.id AND m.kid_id = CAST(sqlc.arg(kid) AS INTEGER)))
ORDER BY i.occurrence_date, i.due_at, c.name, i.seq, i.kid_id;

-- name: ListChoreInstanceMembersBetween :many
SELECT m.instance_id, m.kid_id FROM chore_instance_members m
JOIN chore_instances i ON i.id = m.instance_id
WHERE i.occurrence_date >= ? AND i.occurrence_date <= ?
ORDER BY m.instance_id, m.kid_id;
