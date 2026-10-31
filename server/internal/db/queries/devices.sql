-- name: CreatePairingCode :one
INSERT INTO pairing_codes (code_hash, user_id, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPairingCode :one
SELECT * FROM pairing_codes WHERE code_hash = ?;

-- name: UsePairingCode :execrows
UPDATE pairing_codes SET used_at = ? WHERE id = ? AND used_at IS NULL AND expires_at > ?;

-- name: DeleteExpiredPairingCodes :execrows
DELETE FROM pairing_codes WHERE expires_at <= ?;

-- name: CreateDevice :one
INSERT INTO devices (user_id, name, app_version, created_at) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetDevice :one
SELECT * FROM devices WHERE id = ?;

-- name: SetDeviceElevateFailures :exec
UPDATE devices SET elevate_failures = ?, elevate_locked_until = ? WHERE id = ?;

-- name: ListProfileKids :many
SELECT id, slug, display_name, color, avatar, pin_required, pin_hash IS NOT NULL AS pin_set FROM users
WHERE role = 'child' AND archived_at IS NULL ORDER BY display_name;

-- name: ListElevatableParents :many
SELECT id, display_name FROM users
WHERE role = 'parent' AND removed_at IS NULL AND parent_pin_hash IS NOT NULL ORDER BY display_name;
