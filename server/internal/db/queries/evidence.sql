-- name: CountChoreInstanceEvidence :one
SELECT COUNT(*) FROM chore_instance_evidence WHERE instance_id = ?;

-- name: InsertChoreInstanceEvidence :one
INSERT INTO chore_instance_evidence (instance_id, attempt, kid_id, submitted_by, checklist, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: AttachEvidenceMedia :execrows
-- Moves a pending photo to an evidence row. The guards repeat the checks made before, so a photo
-- attached or expired meanwhile isn't taken: 0 rows = not attachable.
UPDATE media SET owner_kind = 'evidence', owner_id = sqlc.arg(evidence_id), expires_at = NULL
WHERE id = sqlc.arg(id) AND owner_kind = 'pending' AND status = 'ready' AND expires_at > sqlc.arg(now);

-- name: InsertChoreInstanceEvidencePhoto :exec
INSERT INTO chore_instance_evidence_photos (media_id, evidence_id, position, role, captured_at, capture_id)
VALUES (?, ?, ?, ?, ?, ?);

-- name: SetChoreInstanceEvidenceExpiry :exec
-- The instance resolved: its evidence media expires at expires_at, unless a parent keeps it.
UPDATE media SET expires_at = sqlc.arg(expires_at)
WHERE owner_kind = 'evidence' AND status = 'ready' AND keep = 0
  AND owner_id IN (SELECT e.id FROM chore_instance_evidence e WHERE e.instance_id = sqlc.arg(instance_id));

-- name: ClearChoreInstanceEvidenceExpiry :exec
-- The instance is open again: its evidence media no longer expires.
UPDATE media SET expires_at = NULL
WHERE owner_kind = 'evidence' AND status = 'ready'
  AND owner_id IN (SELECT e.id FROM chore_instance_evidence e WHERE e.instance_id = ?);

-- name: ListChoreInstanceEvidence :many
SELECT * FROM chore_instance_evidence WHERE instance_id = ? ORDER BY attempt;

-- name: ListChoreInstanceEvidencePhotos :many
SELECT sqlc.embed(p), sqlc.embed(m) FROM chore_instance_evidence_photos p
JOIN chore_instance_evidence e ON e.id = p.evidence_id
JOIN media m ON m.id = p.media_id
WHERE e.instance_id = ?
ORDER BY p.evidence_id, p.position;
