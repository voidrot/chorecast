-- name: ListChores :many
SELECT * FROM chores ORDER BY disabled, name;

-- name: GetChore :one
SELECT * FROM chores WHERE id = ?;

-- name: CreateChore :one
INSERT INTO chores (name, description, points, penalty, estimated_minutes, min_age, steps, slots,
    schedule, requires_approval, escalate, auto_approve_hours, evidence_photos, evidence_before_after, evidence_video,
    evidence_checklist, evidence_note, location_id, disabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateChore :exec
UPDATE chores SET name = ?, description = ?, points = ?, penalty = ?, estimated_minutes = ?, min_age = ?,
    steps = ?, slots = ?, schedule = ?, requires_approval = ?, escalate = ?, auto_approve_hours = ?, evidence_photos = ?,
    evidence_before_after = ?, evidence_video = ?, evidence_checklist = ?, evidence_note = ?, location_id = ?,
    disabled = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteChore :execrows
DELETE FROM chores WHERE id = ?;

-- name: ListChoreEquipment :many
SELECT chore_id, equipment_id FROM chore_equipment ORDER BY chore_id, equipment_id;

-- name: DeleteChoreEquipment :exec
DELETE FROM chore_equipment WHERE chore_id = ?;

-- name: AddChoreEquipment :exec
INSERT INTO chore_equipment (chore_id, equipment_id) VALUES (?, ?);

-- name: ListChoreEligibility :many
SELECT chore_id, kid_id, allow FROM chore_eligibility ORDER BY chore_id, kid_id;

-- name: DeleteChoreEligibility :exec
DELETE FROM chore_eligibility WHERE chore_id = ?;

-- name: AddChoreEligibility :exec
INSERT INTO chore_eligibility (chore_id, kid_id, allow) VALUES (?, ?, ?);

-- name: ListLocations :many
SELECT * FROM locations ORDER BY name;

-- name: CreateLocation :one
INSERT INTO locations (name, description, created_at) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateLocation :execrows
UPDATE locations SET name = ?, description = ? WHERE id = ?;

-- name: DeleteLocation :execrows
DELETE FROM locations WHERE id = ?;

-- name: ListEquipment :many
SELECT * FROM equipment ORDER BY name;

-- name: CreateEquipment :one
INSERT INTO equipment (name, description, created_at) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateEquipment :execrows
UPDATE equipment SET name = ?, description = ? WHERE id = ?;

-- name: DeleteEquipment :execrows
DELETE FROM equipment WHERE id = ?;
