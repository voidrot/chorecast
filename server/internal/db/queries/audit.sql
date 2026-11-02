-- name: InsertAuditEntry :execrows
-- One row per event: re-running for the same event writes nothing (audit_log_once); rows without an event never conflict.
INSERT INTO audit_log (actor_kind, actor_id, action, target_kind, target_id, summary, data, event_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListAuditEntries :many
-- Newest first, before the cursor id. Empty filters match everything; since and until bound created_at.
SELECT a.id, a.actor_kind, a.actor_id, a.action, a.target_kind, a.target_id, a.summary, a.data, a.created_at,
       COALESCE(u.display_name, '') AS actor_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE a.id < sqlc.arg(before)
  AND (CAST(sqlc.arg(actor_kind) AS TEXT) = '' OR a.actor_kind = sqlc.arg(actor_kind))
  AND (CAST(sqlc.arg(actor_id) AS INTEGER) = 0 OR a.actor_id = sqlc.arg(actor_id))
  AND (CAST(sqlc.arg(action) AS TEXT) = '' OR a.action = sqlc.arg(action))
  AND a.created_at >= sqlc.arg(since) AND a.created_at < sqlc.arg(until)
ORDER BY a.id DESC
LIMIT sqlc.arg(lim);

-- name: DeleteAuditEntriesBefore :execrows
DELETE FROM audit_log WHERE created_at < ?;
