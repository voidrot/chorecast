-- name: CreateToken :one
INSERT INTO tokens (token_hash, kind, user_id, label, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTokenByHash :one
SELECT * FROM tokens WHERE token_hash = ?;

-- name: ResolveToken :one
SELECT t.id, t.kind, t.user_id, t.expires_at, t.last_used_at, u.role, u.is_owner
FROM tokens t
LEFT JOIN users u ON u.id = t.user_id
WHERE t.token_hash = ?;

-- name: TouchToken :exec
UPDATE tokens SET last_used_at = ? WHERE id = ?;

-- name: DeleteToken :exec
DELETE FROM tokens WHERE id = ?;

-- name: DeleteTokensForUser :exec
DELETE FROM tokens WHERE user_id = ?;

-- name: DeleteExpiredTokens :execrows
DELETE FROM tokens WHERE expires_at IS NOT NULL AND expires_at <= ?;
