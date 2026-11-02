# Approvals & evidence

Status: accepted (planning session, 2026-10-30). Builds on [points.md](points.md) (lifecycle, AI check) and
[schedules.md](schedules.md) (instances).

## Decisions

| Topic | Decision |
|---|---|
| Evidence types | Per chore, any combination of: **photos** (min count, optional before + after), **video**, **checklist ticks** (all steps), **note/text**. |
| Rejection | **Back to `open` with required feedback**: quick-pick reason and/or text, plus optional **annotated photo** (draw + text on the kid's photo). Kid can resubmit until day close. |
| Fresh evidence only | **In-app capture only** for kids (no gallery picker); duplicate/reused media is detected and flagged. See below. |
| Past evidence | No gallery. Kids and parents can view the media attached to any past instance from its history entry, until retention expires. |
| Review queue | **Bulk approve**, **approve/reject from the Android notification**, **auto-approve after a timeout**. Any parent can approve; no delegated approver role in v1. |
| Auto-approve | **Household default timeout + per-chore override** (including "never"). An AI verdict of `not_yet` **blocks** auto-approval, so a parent must decide. |
| Offline | **Queue and sync.** The app stores completions and media locally and uploads later with the device's completion time. |
| Storage | **Local disk by default, S3-compatible optional** (MinIO, Garage, R2, AWS). |
| Image processing | Re-encode to ~1600 px **WebP**, EXIF/GPS stripped, WebP thumbnail. No JPEG fallback (minSdk 26 and all supported browsers decode WebP). |
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

## Fresh-evidence rules (no reusing old photos)

Goal: a kid can't satisfy today's chore with an old photo or one taken from the gallery or downloaded.

1. **In-app capture only.** On kid sessions the app uses CameraX directly; there's no photo picker or share-in.
   Each capture gets a `capture_id` and device timestamp recorded by the app at the shutter.
2. **Capture window.** The server rejects evidence whose capture time is before the instance became available
   (start of its day, or of the week for flexible chores). For offline uploads the device timestamp is used, with the
   same clock-skew flag as completions.
3. **Duplicate detection.** On upload the server computes a SHA-256 and a 64-bit perceptual hash (dHash) of the
   decoded image (for video: of the sampled frames when ffmpeg is present).
   - **Exact match** (same SHA-256) against any earlier evidence in the household → **rejected** at upload.
   - **Near-identical** (dHash Hamming distance ≤ 4, tunable) to the same kid's evidence for the same chore in the
     last 30 days → accepted but **flagged** "looks like a previous photo". Flagged submissions **block
     auto-approve** and show the earlier photo side by side in the review queue.
   - Near-duplicates are only flagged, not rejected, because a correctly made bed photographed from the
     same spot every day genuinely looks the same.
4. Hashes are kept after media expires, so reuse is still detected after retention deletes the file.
5. Parents' uploads (reference photos, annotations) use the normal picker; rules 1–3 apply to kid evidence only.

## Rejection annotations

The parent app and web admin open the kid's photo in an editor with **freehand drawing** (a few colors and widths)
and **text labels**. The annotated result is flattened to a new WebP and stored as rejection media
(`owner_kind = rejection`); the original evidence is untouched. Retention follows the instance.

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
       → image: decode → SHA-256 + dHash → resize 1600px → strip metadata → encode WebP → thumbnail 320px
       → video: (ffmpeg?) transcode 720p H.264/AAC, cap 60s, faststart, poster frame : store original
       → write to storage backend → media row (status ready)
```

- Background job queue in-process (SQLite-backed `jobs` table) with **concurrency 1 for transcoding** so a Pi
  stays responsive; images are processed inline.
- `media(id, owner_kind, owner_id, storage_key, kind, mime, bytes, width, height, duration_ms, status,
  keep, created_at, expires_at)`. `duration_ms` is added with video; the photo-only table (#75) omits it. Also used for chore reference photos and equipment photos (these never expire).
- Storage interface: `Put/Get/Delete/SignedURL`. Local implementation stores under `$DATA_DIR/media/aa/bb/<id>`.
  The S3 implementation uses the minio-go client and works with any S3-compatible service.
- **Retention job** (daily): delete media where `expires_at < now AND keep = false`; the evidence row remains
  with a "media expired" marker.
- Backups: local media lives in the data dir, so one directory captures everything. With S3, backing up media is
  the bucket's responsibility; documented.

Built (#75, `internal/media`): the photo half of the pipeline, local storage only.
- `POST /media/photos` (multipart `file`; kids and parents, scope `chores:complete`). The format comes from the
  magic bytes (JPEG, PNG, WebP; HEIC/AVIF and anything else is 415), the size cap from
  `CHORECAST_MEDIA_MAX_PHOTO_MB` (413 `payload_too_large`). Before decoding, the header must show at most 24 MP and
  12,000 px a side (413 `image_too_large`), and the memory decoding needs, estimated per format (progressive and
  CMYK JPEG, 16-bit and interlaced PNG, lossless WebP cost more), must fit 128 MB (413 `image_too_complex`).
  Animated WebP is 415. The photo is scaled to ≤1600 px (box averaging, so no large working buffers), turned
  upright from its EXIF orientation, and a ≤320 px thumbnail is made; both are re-encoded as lossy WebP holding
  pixels only (no EXIF, GPS, XMP or ICC). WebP encoding is pure Go (`github.com/gen2brain/vpx`, single-threaded,
  with decoder panics turned into 422 `invalid_image`), so the `CGO_ENABLED=0` build keeps working. One upload
  peaks at about 220 MB in the worst accepted case (85 MB for a 24 MP JPEG); `deploy/compose.yaml` suggests a
  `GOMEMLIMIT`.
- SHA-256 is of the decoded pixels (dimensions, then RGBA rows), so re-saving a photo with other metadata doesn't
  change it; dHash is stored for later near-duplicate flagging. A kid's upload that matches `media_hashes` is 409
  `duplicate_media`, answered after decoding but before encoding; kid uploads are recorded there, which outlives
  the `media` row. Parents' uploads are neither checked nor recorded.
- New media is `owner_kind = 'pending'` (no owner id) until #27 attaches it. `GET /media/{id}` returns it with fresh
  signed links to the photo and thumbnail: parents see all media, a kid only pending media they uploaded, anyone
  else gets 404 `media_not_found`.
- Signed links (`GET /media/files/{key}?exp&sig`, public: the signature is the credential) last 10 minutes; a link
  claiming a later expiry is refused even if signed. The HMAC-SHA256 key is derived from a random secret created on
  first use in the `server_keys` table (row `media_url`), so **database backups contain it**: anyone holding a
  backup can mint links (they can also read the media files in the same data dir). Rotate it with
  `chorecast admin rotate-media-key` and restart the server; links handed out before stop working. A wrong
  or expired signature is 403 `invalid_media_url`; media whose row is gone, whose status is `expired`, or whose file
  is missing is 410 `media_expired`. Files are served with `Cache-Control: private` and `nosniff`.
- Files live under `$CHORECAST_DATA_DIR/media/aa/bb/<key>` (`<key>_t` for the thumbnail), keyed by a random
  128-bit hex key and written to a temp file, then renamed. Photo processing runs one at a time; up to 4 uploads
  wait for it without reading their bodies into memory, and more get 503 `media_busy` with `Retry-After`.
  (`net/http` spools multipart bodies over 8 KB to the temp dir first, so the server needs a writable `/tmp`.)

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

- None outstanding from the planning session. Tune the dHash threshold with real household photos during beta.
