-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY role, display_name;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (display_name, role, is_owner, slug, username, password_hash, pin_hash, pin_required, color, birth_date, avatar)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetKidByID :one
SELECT * FROM users WHERE id = ? AND role = 'child';

-- name: GetKidBySlug :one
SELECT * FROM users WHERE slug = ? AND role = 'child';

-- name: SlugTaken :one
SELECT EXISTS (SELECT 1 FROM users WHERE slug = ?);
