-- name: GetParentByUsername :one
SELECT * FROM users WHERE username = ? AND role = 'parent';

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = ? WHERE id = ?;

-- name: SetParentPINHash :exec
UPDATE users SET parent_pin_hash = ? WHERE id = ?;

-- name: SetPendingTOTPSecret :exec
UPDATE users SET totp_secret = ?, totp_enabled = 0, totp_last_step = 0 WHERE id = ?;

-- name: EnableTOTP :exec
UPDATE users SET totp_enabled = 1, totp_last_step = ? WHERE id = ?;

-- name: DisableTOTP :exec
UPDATE users SET totp_secret = NULL, totp_enabled = 0, totp_last_step = 0 WHERE id = ?;

-- name: SetTOTPLastStep :exec
UPDATE users SET totp_last_step = ? WHERE id = ?;

-- name: DeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = ?;

-- name: InsertRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?);

-- name: UseRecoveryCode :execrows
UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL;

-- name: CountUnusedRecoveryCodes :one
SELECT count(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL;

-- name: ListSessionsForUser :many
SELECT id, kind, label, created_at, last_used_at, expires_at FROM tokens
WHERE user_id = ? AND kind IN ('web_session', 'app') AND (expires_at IS NULL OR expires_at > ?)
ORDER BY coalesce(last_used_at, created_at) DESC;

-- name: GetTokenForUser :one
SELECT id, kind FROM tokens WHERE id = ? AND user_id = ?;

-- name: DeleteOtherSessionsForUser :exec
DELETE FROM tokens WHERE user_id = ? AND kind IN ('web_session', 'app') AND id != ?;
