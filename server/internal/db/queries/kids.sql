-- name: ListKids :many
SELECT * FROM users WHERE role = 'child' AND (sqlc.arg(include_archived) OR archived_at IS NULL)
ORDER BY archived_at IS NOT NULL, display_name;

-- name: UpdateKid :exec
UPDATE users SET display_name = ?, slug = ?, color = ?, avatar = ?, birth_date = ?, pin_required = ?,
    day_end_override = ?, quiet_start = ?, quiet_end = ?, school_days = ?, school_start = ?, school_end = ?
WHERE id = ? AND role = 'child';

-- name: SetKidPIN :exec
UPDATE users SET pin_hash = ? WHERE id = ? AND role = 'child';

-- name: SetKidArchived :exec
UPDATE users SET archived_at = ? WHERE id = ? AND role = 'child';

-- name: SlugTakenByOther :one
SELECT EXISTS (SELECT 1 FROM users WHERE slug = ? AND id != ?);
