-- name: InsertServerKey :exec
INSERT INTO server_keys (name, key, created_at) VALUES (?, ?, ?)
ON CONFLICT (name) DO NOTHING;

-- name: ReplaceServerKey :exec
INSERT INTO server_keys (name, key, created_at) VALUES (?, ?, ?)
ON CONFLICT (name) DO UPDATE SET key = excluded.key, created_at = excluded.created_at;

-- name: GetServerKey :one
SELECT key FROM server_keys WHERE name = ?;

-- name: CreateMedia :one
INSERT INTO media (uploaded_by, storage_key, thumb_key, kind, mime, bytes, width, height,
                   thumb_width, thumb_height, sha256, dhash, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMedia :one
SELECT * FROM media WHERE id = ?;

-- name: GetMediaHash :one
SELECT * FROM media_hashes WHERE sha256 = ?;

-- name: RecordMediaHash :one
INSERT INTO media_hashes (sha256, dhash, kid_id, media_id, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (sha256) DO NOTHING
RETURNING sha256;

-- name: GetMediaByKey :one
-- The media whose full-size file or thumbnail is stored under key.
SELECT * FROM media WHERE storage_key = sqlc.arg(key) OR thumb_key = sqlc.arg(key);

-- name: CountPendingUploads :one
-- A user's pending uploads that can still be attached (the pending upload cap).
SELECT COUNT(*) FROM media
WHERE uploaded_by = sqlc.arg(uploaded_by) AND owner_kind = 'pending' AND status = 'ready' AND expires_at > sqlc.arg(now);

-- name: ListExpiredMedia :many
-- Media whose files retention deletes: expired and not kept, or evidence whose record is gone
-- (its instance was deleted along with its kid).
SELECT * FROM media
WHERE status = 'ready'
  AND ((keep = 0 AND expires_at < sqlc.arg(now))
       OR (owner_kind = 'evidence' AND NOT EXISTS (SELECT 1 FROM chore_instance_evidence e WHERE e.id = media.owner_id)))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: MarkMediaExpired :execrows
-- After its files are deleted. Repeats ListExpiredMedia's condition, so media that stopped
-- expiring meanwhile (its instance reopened) stays ready.
UPDATE media SET status = 'expired'
WHERE media.id = sqlc.arg(id) AND media.status = 'ready'
  AND ((keep = 0 AND expires_at < sqlc.arg(now))
       OR (owner_kind = 'evidence' AND NOT EXISTS (SELECT 1 FROM chore_instance_evidence e WHERE e.id = media.owner_id)));

-- name: DeleteMediaHashOf :exec
-- Abandoned pending media was never evidence, so its hash mustn't block a later upload.
DELETE FROM media_hashes WHERE media_id = ?;
