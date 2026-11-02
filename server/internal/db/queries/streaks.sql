-- name: GetKidStreak :one
SELECT * FROM kid_streaks WHERE kid_id = ?;

-- name: UpsertKidStreak :exec
INSERT INTO kid_streaks (kid_id, current, best, last_day) VALUES (?, ?, ?, ?)
ON CONFLICT (kid_id) DO UPDATE SET current = excluded.current, best = excluded.best, last_day = excluded.last_day;

-- name: ListStreakDays :many
-- The kid's evaluated days from the day on, oldest first.
SELECT day, outcome FROM streak_days WHERE kid_id = ? AND day >= ? ORDER BY day;

-- name: UpsertStreakDay :exec
INSERT INTO streak_days (kid_id, day, outcome) VALUES (?, ?, ?)
ON CONFLICT (kid_id, day) DO UPDATE SET outcome = excluded.outcome;

-- name: TallyStreakDays :many
-- Per day in [from_day, to_day]: the kid's required instances (their own and shared ones they're a
-- member of; flexible ones belong to their week, not a day) and how many are done. Days without
-- any are left out.
SELECT i.occurrence_date,
    COUNT(*) AS total,
    CAST(TOTAL(i.status IN ('submitted', 'approved', 'excused')) AS INTEGER) AS done
FROM chore_instances i
WHERE i.flexible = 0
  AND i.occurrence_date >= sqlc.arg(from_day) AND i.occurrence_date <= sqlc.arg(to_day)
  AND (i.kid_id = CAST(sqlc.arg(kid) AS INTEGER)
    OR EXISTS (SELECT 1 FROM chore_instance_members m WHERE m.instance_id = i.id AND m.kid_id = CAST(sqlc.arg(kid) AS INTEGER)))
GROUP BY i.occurrence_date
ORDER BY i.occurrence_date;

-- name: ListChoreStreakHistory :many
-- The kid's non-flexible instances of recurring chores (any schedule but "once") dated up to the
-- day, in occurrence order per chore.
SELECT i.chore_id, i.status
FROM chore_instances i
JOIN chores c ON c.id = i.chore_id
WHERE i.flexible = 0
  AND i.occurrence_date <= sqlc.arg(to_day)
  AND c.schedule IS NOT NULL AND json_extract(c.schedule, '$.type') != 'once'
  AND (i.kid_id = CAST(sqlc.arg(kid) AS INTEGER)
    OR EXISTS (SELECT 1 FROM chore_instance_members m WHERE m.instance_id = i.id AND m.kid_id = CAST(sqlc.arg(kid) AS INTEGER)))
ORDER BY i.chore_id, i.occurrence_date, i.due_at, i.id;

-- name: DeleteKidChoreStreaks :exec
DELETE FROM kid_chore_streaks WHERE kid_id = ?;

-- name: InsertKidChoreStreak :exec
INSERT INTO kid_chore_streaks (kid_id, chore_id, current, best) VALUES (?, ?, ?, ?);

-- name: ListKidChoreStreaks :many
SELECT chore_id, current, best FROM kid_chore_streaks WHERE kid_id = ? ORDER BY chore_id;
