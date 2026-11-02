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
-- Ready, unkept media retention takes: past its expiry, or evidence whose record is gone (its
-- instance was deleted along with its kid).
SELECT * FROM media
WHERE status = 'ready' AND keep = 0
  AND (expires_at <= sqlc.arg(now)
       OR (owner_kind = 'evidence' AND NOT EXISTS (SELECT 1 FROM chore_instance_evidence e WHERE e.id = media.owner_id)))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: ClaimExpiredMedia :execrows
-- Claims one item for deletion if it still qualifies (ListExpiredMedia's condition), before its
-- files are deleted. Resolve, Reopen and attaching touch only ready media, so a claimed item
-- can't be revived. 0 rows = it no longer qualifies.
UPDATE media SET status = 'deleting'
WHERE media.id = sqlc.arg(id) AND media.status = 'ready' AND media.keep = 0
  AND (media.expires_at <= sqlc.arg(now)
       OR (media.owner_kind = 'evidence' AND NOT EXISTS (SELECT 1 FROM chore_instance_evidence e WHERE e.id = media.owner_id)));

-- name: DiscardPendingMedia :execrows
-- The uploader gives up a pending photo: claimed for deletion like retention does, if it is still
-- ready and unattached. Attaching touches only ready pending media, so one of the two wins.
UPDATE media SET status = 'deleting'
WHERE id = sqlc.arg(id) AND uploaded_by = sqlc.arg(uploaded_by) AND owner_kind = 'pending' AND status = 'ready';

-- name: ListDeletingMedia :many
-- Claimed media whose files aren't deleted yet: this run's, and any an earlier run couldn't finish.
SELECT * FROM media WHERE status = 'deleting' AND id > sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(max_rows);

-- name: MarkMediaExpired :exec
-- After a claimed item's files are deleted.
UPDATE media SET status = 'expired' WHERE id = ? AND status = 'deleting';

-- name: DeleteMediaHashOf :exec
-- Abandoned pending media was never evidence, so its hash mustn't block a later upload.
DELETE FROM media_hashes WHERE media_id = ?;

-- name: GetKidAvatarMedia :one
-- The kid's current avatar photo.
SELECT * FROM media WHERE owner_kind = 'kid_avatar' AND owner_id = ? AND status = 'ready';

-- name: AttachKidAvatar :exec
-- Makes a just-created pending upload the kid's avatar: no owner expiry, so retention never takes it.
UPDATE media SET owner_kind = 'kid_avatar', owner_id = ?, expires_at = NULL WHERE id = ? AND owner_kind = 'pending';

-- name: ReleaseKidAvatar :many
-- Claims the kid's avatar photo for deletion (status deleting); the caller deletes the files, or
-- the retention job's second step does if that fails.
UPDATE media SET status = 'deleting' WHERE owner_kind = 'kid_avatar' AND owner_id = ? AND status = 'ready'
RETURNING *;
