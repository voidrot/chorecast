-- name: GetHousehold :one
SELECT * FROM household WHERE id = 1;

-- name: HouseholdExists :one
SELECT EXISTS (SELECT 1 FROM household);

-- name: CreateHousehold :exec
INSERT INTO household (id, name, timezone, day_end, week_start, created_at)
VALUES (1, ?, ?, ?, ?, ?);

-- name: SetSetupToken :exec
INSERT INTO setup_token (id, token_hash, created_at) VALUES (1, ?, ?)
ON CONFLICT (id) DO UPDATE SET token_hash = excluded.token_hash, created_at = excluded.created_at;

-- name: GetSetupTokenHash :one
SELECT token_hash FROM setup_token WHERE id = 1;

-- name: DeleteSetupToken :exec
DELETE FROM setup_token;
