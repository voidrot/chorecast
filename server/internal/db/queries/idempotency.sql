-- name: ReserveIdempotencyKey :one
INSERT INTO idempotency_keys (token_id, key, request_hash, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (token_id, key) DO NOTHING
RETURNING token_id;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE token_id = ? AND key = ?;

-- name: CompleteIdempotencyKey :exec
UPDATE idempotency_keys SET status = ?, content_type = ?, body = ?
WHERE token_id = ? AND key = ?;

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys WHERE token_id = ? AND key = ?;

-- name: DeleteIdempotencyKeysBefore :execrows
DELETE FROM idempotency_keys WHERE created_at < ?;
