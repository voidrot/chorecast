-- name: CreateToken :one
INSERT INTO tokens (token_hash, kind, user_id, label, expires_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTokenByHash :one
SELECT * FROM tokens WHERE token_hash = ?;

-- name: DeleteToken :exec
DELETE FROM tokens WHERE id = ?;
