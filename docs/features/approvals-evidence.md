# Approvals & evidence

Status: accepted (planning session, 2026-10-30). Builds on [points.md](points.md) (lifecycle, AI check) and
[schedules.md](schedules.md) (instances).

## Decisions

| Topic | Decision |
|---|---|
| Evidence types | Per chore, any combination of: **photos** (min count, optional before + after), **video**, **checklist ticks** (all steps), **note/text**. |
| Rejection | **Back to `open` with required feedback** (quick-pick reason or text, optional annotated photo). Kid can resubmit until day close. |
| Review queue | **Bulk approve**, **approve/reject from the Android notification**, **auto-approve after a timeout**. Any parent can approve; no delegated approver role in v1. |
| Auto-approve | **Household default timeout + per-chore override** (including "never"). An AI verdict of `not_yet` **blocks** auto-approval, so a parent must decide. |
| Offline | **Queue and sync.** The app stores completions and media locally and uploads later with the device's completion time. |
| Storage | **Local disk by default, S3-compatible optional** (MinIO, Garage, R2, AWS). |
| Image processing | Re-encode to ~1600 px, EXIF/GPS stripped, thumbnail generated. Pure Go. |
| Video processing | **ffmpeg optional, detected at runtime.** If present: transcode to **720p H.264/AAC MP4, max 60 s**, poster frame. If absent: stored as uploaded, size/length capped, no poster. |
| Images published | `ghcr.io/voidrot/chorecast:<v>` (slim, distroless) and `:<v>-media` (with ffmpeg). |
| AI on video | ~4 frames sampled evenly via ffmpeg and sent as images; skipped when ffmpeg is unavailable. |
| Media access | **Short-lived signed URLs** (~10 min): server-HMAC-signed for local disk, presigned for S3. |
| Retention | **Household setting, default 30 days** after the instance is resolved; parents can **keep** (favorite) individual items. Records, notes, verdicts stay. |
| Limits | Defaults overridable by env: photo 15 MB, video 100 MB / 60 s, 5 files per submission. |

## Submission flow

1. Kid opens the instance. If `ai_check_enabled`, they can take a photo and get coaching feedback first
   (see points.md), as many tries as the rate limit allows.
2. Kid satisfies the chore's evidence requirements (client validates; server re-validates), then submits.
3. `requires_approval = false`: instance → `approved`, points credited. Evidence is still kept for review.
   `requires_approval = true`: instance → `submitted`, parents are notified.
4. Parent approves (→ `approved`, points), or rejects with feedback (→ `open`, kid notified). Rejection count and
   feedback history are kept on the instance.
5. Auto-approve: a job approves `submitted` instances older than the effective timeout, unless the latest AI
   verdict is `not_yet`. The ledger entry is marked `created_by = system` with an "auto-approved" note.

## Offline sync

- The app keeps a local outbox (Room DB + files). Each submission has a client-generated UUID, so retries are
  idempotent (`Idempotency-Key`).
- `completed_at` is the device time. The server accepts it if it is ≤ the instance's day close + **grace margin**
  (default 2 h) *and* the upload arrives within 48 h. Otherwise the instance follows normal missed rules and a
  parent can excuse it. Large clock skew vs. server time is flagged to parents.
- Media upload is resumable per file (chunked `PUT` with offsets) so a 100 MB video over flaky Wi-Fi works.
- The app also downscales photos before upload to save bandwidth; the server re-processes regardless.

## Media pipeline

```
upload → quarantine/tmp → validate (magic bytes, size, duration)
       → image: decode → resize 1600px → strip metadata → encode WebP (+ JPEG fallback?) → thumbnail 320px
       → video: (ffmpeg?) transcode 720p H.264/AAC, cap 60s, faststart, poster frame : store original
       → write to storage backend → media row (status ready)
```

- Background job queue in-process (SQLite-backed `jobs` table) with **concurrency 1 for transcoding** so a Pi
  stays responsive; images are processed inline.
- `media(id, owner_kind, owner_id, storage_key, kind, mime, bytes, width, height, duration_ms, status,
  keep, created_at, expires_at)`. Also used for chore reference photos and equipment photos (these never expire).
- Storage interface: `Put/Get/Delete/SignedURL`. Local implementation stores under `$DATA_DIR/media/aa/bb/<id>`.
  The S3 implementation uses the minio-go client and works with any S3-compatible service.
- **Retention job** (daily): delete media where `expires_at < now AND keep = false`; the evidence row remains
  with a "media expired" marker.
- Backups: local media lives in the data dir, so one directory captures everything. With S3, backing up media is
  the bucket's responsibility; documented.

## Review queue (parent)

- List of `submitted` instances, oldest first, grouped by kid, with thumbnails, AI verdict chip, checklist and note.
- Filters: kid, chore, "AI says done", "no media".
- Bulk select → approve. Bulk reject isn't offered, because rejection needs per-item feedback.
- Android notification for each submission (or batched if several) with **Approve** and **Reject** actions.
  Reject opens a quick-reason sheet. The actions call the API with the parent's app token from a background worker.

## Configuration (env)

| Variable | Default |
|---|---|
| `CHORECAST_STORAGE` | `local` (`s3`) |
| `CHORECAST_S3_ENDPOINT`, `_BUCKET`, `_REGION`, `_ACCESS_KEY`, `_SECRET_KEY`, `_PATH_STYLE` | — |
| `CHORECAST_MEDIA_MAX_PHOTO_MB` | 15 |
| `CHORECAST_MEDIA_MAX_VIDEO_MB` | 100 |
| `CHORECAST_MEDIA_MAX_VIDEO_SECONDS` | 60 |
| `CHORECAST_MEDIA_MAX_FILES` | 5 |
| `CHORECAST_FFMPEG_PATH` | auto-detect on `PATH` |

Household settings (in the admin UI, not env): retention days, auto-approve timeout, offline grace margin.

## Open questions

- WebP only, or keep a JPEG fallback for very old Android WebViews? (minSdk 26 supports WebP, so probably WebP only.)
- Should kids be able to see their own past evidence after approval (a "my work" gallery)?
- Annotated rejection photos: draw on the photo in the app (v1) or text only first?
