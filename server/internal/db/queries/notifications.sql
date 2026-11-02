-- name: InsertKidNotification :execrows
-- A kid's notification; nothing for a kid who is archived or gone. Re-running for the same event
-- writes nothing (the unique key).
INSERT INTO notifications (recipient_id, kind, kid_id, ref_kind, ref_id, event_id, data, created_at)
SELECT u.id, CAST(sqlc.arg(kind) AS TEXT), u.id, CAST(sqlc.arg(ref_kind) AS TEXT), CAST(sqlc.arg(ref_id) AS INTEGER),
       CAST(sqlc.arg(event_id) AS INTEGER), CAST(sqlc.arg(data) AS TEXT), CAST(sqlc.arg(created_at) AS TEXT)
FROM users u
WHERE u.id = sqlc.arg(kid) AND u.role = 'child' AND u.archived_at IS NULL
ON CONFLICT DO NOTHING;

-- name: InsertParentNotifications :execrows
-- One notification per parent who is still in the household, except the one who acted.
INSERT INTO notifications (recipient_id, kind, kid_id, ref_kind, ref_id, event_id, data, created_at)
SELECT u.id, CAST(sqlc.arg(kind) AS TEXT), CAST(sqlc.arg(kid) AS INTEGER), CAST(sqlc.arg(ref_kind) AS TEXT),
       CAST(sqlc.arg(ref_id) AS INTEGER), CAST(sqlc.arg(event_id) AS INTEGER), CAST(sqlc.arg(data) AS TEXT),
       CAST(sqlc.arg(created_at) AS TEXT)
FROM users u
WHERE u.role = 'parent' AND u.removed_at IS NULL AND u.id != sqlc.arg(except)
ON CONFLICT DO NOTHING;

-- name: GetInstanceChoreName :one
SELECT c.name FROM chore_instances i JOIN chores c ON c.id = i.chore_id WHERE i.id = ?;

-- name: ListNotifications :many
-- Newest first, before the cursor id.
SELECT * FROM notifications WHERE recipient_id = ? AND id < ? ORDER BY id DESC LIMIT ?;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications WHERE recipient_id = ? AND read_at IS NULL;

-- name: MarkNotificationRead :execrows
-- Keeps the first read time; the row count says whether the recipient has that notification.
UPDATE notifications SET read_at = COALESCE(read_at, sqlc.arg(now)) WHERE id = sqlc.arg(id) AND recipient_id = sqlc.arg(recipient);

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications SET read_at = ? WHERE recipient_id = ? AND read_at IS NULL;

-- name: DeleteNotificationsBefore :execrows
DELETE FROM notifications WHERE created_at < ?;
