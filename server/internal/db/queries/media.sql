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
                   thumb_width, thumb_height, sha256, dhash, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMedia :one
SELECT * FROM media WHERE id = ?;

-- name: MediaHashExists :one
SELECT EXISTS (SELECT 1 FROM media_hashes WHERE sha256 = ?);

-- name: RecordMediaHash :one
INSERT INTO media_hashes (sha256, dhash, kid_id, media_id, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (sha256) DO NOTHING
RETURNING sha256;

-- name: GetMediaByKey :one
-- The media whose full-size file or thumbnail is stored under key.
SELECT * FROM media WHERE storage_key = sqlc.arg(key) OR thumb_key = sqlc.arg(key);
