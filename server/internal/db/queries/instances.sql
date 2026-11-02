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
ORDER BY occurrence_date DESC, due_at DESC, seq DESC
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
SELECT sqlc.embed(i), c.name AS chore_name FROM chore_instances i
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

-- name: ChoreHasHistory :one
-- Whether the chore has an instance to keep: touched, or already due.
SELECT EXISTS (SELECT 1 FROM chore_instances
    WHERE chore_id = ? AND (status != 'open' OR manually_reassigned = 1 OR completed_at IS NOT NULL OR due_at <= ?));

-- name: DeleteChoreInstances :exec
DELETE FROM chore_instances WHERE chore_id = ?;

-- name: GetChoreInstance :one
SELECT sqlc.embed(i), c.name AS chore_name FROM chore_instances i
JOIN chores c ON c.id = i.chore_id
WHERE i.id = ?;

-- name: SetChoreInstanceStatus :execrows
-- Moves an instance from one status to another; 0 rows = it isn't in from_status (any more).
UPDATE chore_instances SET status = sqlc.arg(to_status), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status);

-- name: CompleteChoreInstance :execrows
UPDATE chore_instances SET status = sqlc.arg(to_status), completed_by = sqlc.arg(completed_by),
    completed_at = sqlc.arg(completed_at), updated_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(id) AND status = 'open';

-- name: RejectChoreInstance :execrows
UPDATE chore_instances SET status = 'open', rejection_count = rejection_count + 1, updated_at = ?
WHERE id = ? AND status = 'submitted';

-- name: ReassignChoreInstance :execrows
-- A parent's pick replaces any auto-pick; manually_reassigned keeps the horizon from replanning it,
-- and reassigned_from keeps the kid it was planned for (the first, across several reassignments).
UPDATE chore_instances SET kid_id = sqlc.arg(kid_id), reassigned_from = COALESCE(reassigned_from, kid_id),
    manually_reassigned = 1, auto_assigned = 0, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'open' AND kid_id IS NOT NULL;

-- name: InsertChoreInstanceRejection :exec
INSERT INTO chore_instance_rejections (instance_id, kid_id, reason_code, reason_text, rejected_by, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: LatestChoreInstanceRejection :one
SELECT * FROM chore_instance_rejections WHERE instance_id = ? ORDER BY id DESC LIMIT 1;

-- name: ListLatestChoreInstanceRejectionsBetween :many
-- The latest rejection of each instance on [from, to].
SELECT r.* FROM chore_instance_rejections r
JOIN chore_instances i ON i.id = r.instance_id
WHERE i.occurrence_date >= ? AND i.occurrence_date <= ?
  AND r.id = (SELECT MAX(r2.id) FROM chore_instance_rejections r2 WHERE r2.instance_id = r.instance_id);
