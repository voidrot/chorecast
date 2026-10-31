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

-- name: ListDevices :many
SELECT d.id, d.user_id, d.name, d.app_version, d.created_at,
    coalesce(k.display_name, '') AS kid_name,
    CAST(coalesce((SELECT max(coalesce(t.last_used_at, t.created_at)) FROM tokens t WHERE t.device_id = d.id), d.created_at) AS TEXT) AS last_seen,
    CAST(coalesce((SELECT u.display_name FROM tokens t JOIN users u ON u.id = t.user_id
        WHERE t.device_id = d.id AND t.kind = 'device_session' AND t.expires_at > sqlc.arg(now)
        ORDER BY t.created_at DESC LIMIT 1), '') AS TEXT) AS session_user
FROM devices d LEFT JOIN users k ON k.id = d.user_id
ORDER BY d.created_at DESC;

-- name: DeleteDevice :execrows
DELETE FROM devices WHERE id = ?;

-- name: DeleteDeviceSessions :execrows
DELETE FROM tokens WHERE device_id = ? AND kind = 'device_session';

-- name: SetDeviceAppVersion :exec
UPDATE devices SET app_version = ? WHERE id = ?;
