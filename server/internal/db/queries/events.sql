-- name: InsertEvent :one
INSERT INTO events (type, payload, occurred_at) VALUES (?, ?, ?)
RETURNING id;

-- name: ListUndispatchedEvents :many
SELECT * FROM events WHERE dispatched_at IS NULL ORDER BY id LIMIT ?;

-- name: MarkEventDispatched :exec
UPDATE events SET dispatched_at = ? WHERE id = ?;

-- name: DeleteDispatchedEventsBefore :execrows
DELETE FROM events WHERE dispatched_at IS NOT NULL AND dispatched_at < ?;
