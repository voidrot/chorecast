-- name: ListParents :many
SELECT * FROM users WHERE role = 'parent' AND removed_at IS NULL ORDER BY is_owner DESC, display_name;

-- name: GetParent :one
SELECT * FROM users WHERE id = ? AND role = 'parent' AND removed_at IS NULL;

-- name: CountOwners :one
SELECT count(*) FROM users WHERE role = 'parent' AND is_owner = 1 AND removed_at IS NULL;

-- name: SetOwner :exec
UPDATE users SET is_owner = ? WHERE id = ? AND role = 'parent';

-- name: UsernameTaken :one
SELECT EXISTS (SELECT 1 FROM users WHERE username = ?);

-- name: RemoveParent :exec
-- Keeps the row (and display name) for history; frees the username and drops every credential.
UPDATE users SET removed_at = ?, is_owner = 0, username = NULL, password_hash = NULL, parent_pin_hash = NULL,
    oidc_subject = NULL, totp_secret = NULL, totp_enabled = 0, totp_last_step = 0
WHERE id = ? AND role = 'parent';

-- name: CreateInvite :one
INSERT INTO invites (token_hash, role, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: ListPendingInvites :many
SELECT i.*, u.display_name AS created_by_name FROM invites i LEFT JOIN users u ON u.id = i.created_by
WHERE i.used_at IS NULL AND i.expires_at > ? ORDER BY i.created_at DESC;

-- name: GetInviteByHash :one
SELECT i.*, u.display_name AS created_by_name FROM invites i LEFT JOIN users u ON u.id = i.created_by
WHERE i.token_hash = ?;

-- name: DeleteInvite :execrows
DELETE FROM invites WHERE id = ? AND used_at IS NULL;

-- name: UseInvite :execrows
UPDATE invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL AND expires_at > ?;

-- name: DeleteExpiredInvites :execrows
DELETE FROM invites WHERE used_at IS NULL AND expires_at <= ?;
