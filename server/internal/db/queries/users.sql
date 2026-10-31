-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY role, display_name;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (display_name, role, username, password_hash, pin_hash)
VALUES (?, ?, ?, ?, ?)
RETURNING *;
