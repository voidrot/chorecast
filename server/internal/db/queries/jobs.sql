-- name: EnqueueJob :one
INSERT INTO jobs (queue, kind, payload, idempotency_key, run_at, max_attempts, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id;

-- name: ClaimJob :one
UPDATE jobs
SET status = 'running', attempts = attempts + 1, locked_at = sqlc.arg(now)
WHERE id = (
    SELECT j.id FROM jobs j
    WHERE j.queue = sqlc.arg(queue) AND j.status = 'queued' AND j.run_at <= sqlc.arg(now)
    ORDER BY j.run_at, j.id
    LIMIT 1
)
RETURNING *;

-- name: CompleteJob :exec
UPDATE jobs SET status = 'done', finished_at = ?, last_error = NULL WHERE id = ?;

-- name: RetryJob :exec
UPDATE jobs SET status = 'queued', run_at = ?, last_error = ?, locked_at = NULL WHERE id = ?;

-- name: FailJob :exec
UPDATE jobs SET status = 'failed', finished_at = ?, last_error = ? WHERE id = ?;

-- name: RequeueRunningJobs :execrows
UPDATE jobs SET status = 'queued', locked_at = NULL WHERE status = 'running';

-- name: DeleteFinishedJobs :execrows
DELETE FROM jobs
WHERE (status = 'done' AND finished_at < sqlc.arg(done_before))
   OR (status = 'failed' AND finished_at < sqlc.arg(failed_before));

-- name: GetJobByKey :one
SELECT * FROM jobs WHERE idempotency_key = ?;

-- name: GetScheduleCursor :one
SELECT last_fired_at FROM schedule_cursors WHERE name = ?;

-- name: SetScheduleCursor :exec
INSERT INTO schedule_cursors (name, last_fired_at) VALUES (?, ?)
ON CONFLICT (name) DO UPDATE SET last_fired_at = excluded.last_fired_at;
