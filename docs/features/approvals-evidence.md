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

Built (#25, `internal/instances`; evidence is #27). Steps 3 and 4 are `instances.Complete`, `Approve` and `Reject`
(see points.md, "Chore instance lifecycle", for the API and the ledger rules). `Complete` checks and attaches
the evidence (`checkEvidence`; see "Built (#27)" below). A rejection needs a quick-pick `reason` (`not_done`,
`not_finished`, `needs_redo` or `evidence_unclear`), a `text` of up to 500 characters, or both (422 otherwise).
Every rejection is kept in `chore_instance_rejections` (reason, text, the rejected completer, the parent, when),
and the instance carries `rejection_count`; the API returns `rejectionCount` and `lastRejection`. A rejection
after the instance's day has closed makes it `missed`, with its penalty, instead of `open`, since the kid can't
redo it. For auto-approve (#29): `instances.Approve(ctx, tx, clock, id, 0, "auto-approved")` posts the credit with
`created_by` NULL and that note, and `ErrNotSubmitted` means someone already decided, so the job skips it.

Built (#29, `internal/autoapprove`, server only). Step 5 is the job kind `autoapprove.run`, scheduled every 5
minutes (`jobs.Every`, wired in `background.go`). Each run lists the `submitted` instances already past their timeout (decided in SQL) and approves, one
transaction each, those whose latest submission (`completed_at`; a resubmission after a rejection restarts the
clock) is older than the effective timeout. The timeout is the chore's `auto_approve_hours` (NULL = household
default, **0 = never**, N = N hours), else the household's `auto_approve_hours` (NULL = never). Both are read at
each run, so a settings change applies on the next tick, and a run after downtime approves everything overdue.
The approval is `instances.Approve(..., 0, "auto-approved")`: the credit has `created_by` NULL and that note, and
the usual `instance.approved` event (`by` 0) puts it in the audit log (#37) with the system as the actor. The job
re-reads each instance inside its transaction and skips one that was decided, or resubmitted, since the listing,
so repeated or concurrent runs are no-ops. `blocked()` is the seam for Phase 3 blockers (an AI `not_yet`
verdict, a near-duplicate flag); it returns false today. A partial index `chore_instances_submitted` on
`completed_at` serves the listing.

Built (#27, `internal/evidence`, server only; Android capture is #31, the review queue #28).
- **Submitting.** `POST /instances/{id}/completion` takes `checklist` (indexes of the ticked steps), `note`
  (≤500 characters) and `photos` (≤5: `mediaId` of a pending upload, `role` `before`/`after`, and for a kid's photo
  the `capturedAt` device time and `captureId` the app recorded at the shutter). `evidence.Attach` runs inside the
  completing transaction and answers 422 `validation_failed` with the field's location when a rule fails: an
  unticked step or an unknown index (`body.checklist[i]`), a blank required note, fewer photos than the chore asks
  for, no `before` and `after` when it asks for both, a photo listed twice, or a photo that isn't attachable. A photo
  is attachable when it is pending, still within its 24 h, and was uploaded by the completing kid or by the parent
  completing for them; anyone else's is answered as "no such photo". Nothing is written when a rule fails.
- **Capture window** (rule 2): a kid's photo must be captured at or after `dayclose.OpensAt`, the close of the day
  before the instance's day (for its kid's effective day end, or the household's for a shared instance), or before
  its week for a flexible chore. Parents' photos are exempt (rule 5). Any photo's `capturedAt`, a parent's
  included, must be a storable time no later than now + 10 minutes (`evidence.MaxCaptureSkew`), else 422 at
  `body.photos[i].capturedAt`. Otherwise the device time is trusted as sent; the offline clock-skew flag (#35) is
  to widen or flag the window at `OpensAt` and the skew allowance.
- **Video** (Phase 3): chores can't ask for a video yet: create and update refuse `evidence.video = true` with 422
  `video_evidence_unsupported`, and the web form disables the checkbox ("coming later"; it stays untickable for a
  chore that already has it). A chore that has it anyway can't be completed (the same 422), for kids and parents.
- **Records.** Each completion adds a `chore_instance_evidence` row (`attempt` 1, 2, … per instance, the kid, who
  submitted it, the ticks, the note), so a resubmission after a rejection keeps the earlier set. Its photos move to
  `owner_kind = 'evidence'` with `owner_id` = that row, and `chore_instance_evidence_photos` keeps each one's
  order, role, capture time and capture id. A photo can be attached once, and its picture can't be uploaded by a kid
  again (409 `duplicate_media`), so the same photo can't serve two instances. An instance with evidence counts as
  touched for the horizon and for deleting its chore.
- **Reading.** `GET /instances/{id}/evidence` (`chores:read`) returns submissions oldest first. Parents see every
  submission; the members of a shared instance see the group's; any other kid sees only the submissions they made,
  so a kid an instance was reassigned to doesn't see the earlier kid's, and the earlier kid keeps theirs. A kid with
  no submission who isn't the assignee, a display or a device gets 403. Photos carry signed `url` and `thumbUrl`
  links (10 minutes), or `expired: true` and no links once retention has taken them; a stored capture time that
  doesn't parse is left out instead of failing the read. In Go, `evidence.ReadFor(ctx, q, principal, instance,
  signer)` is that scoped read, and `evidence.List(ctx, q, instanceID, signer)` the unscoped one for parents' views
  (the review queue). Attached photos aren't readable through `GET /media/{id}` by kids; this endpoint is their path.
- **Retention.** When an instance resolves (approved, also on completion when no approval is needed; missed;
  excused) its evidence photos get `expires_at` = then + `media_retention_days`, unless `keep` is set; a reversed
  approval clears it, and a later resolution restarts it. Pending uploads get `expires_at` = upload + 24 h
  (`media.PendingTTL`), cleared when attached. The daily job `media.retention` (`media.Expire`) takes every ready,
  unkept item past `expires_at`, and unkept evidence whose record is gone (its instance was deleted with its kid).
  It works in two steps, so a parent reopening an instance mid-run can't leave a ready photo without files:
  1. *Claim*: in a transaction that re-checks the item still qualifies, it becomes status `deleting`. Resolve,
     Reopen and attaching touch only `ready` media, so a claimed item stays claimed, and reads show it expired.
  2. *Delete*: the files of every `deleting` item (this run's, and any an earlier run left after a crash or a
     failed delete) are deleted and the item becomes `expired`. A file that can't be deleted is logged, counted
     and retried by the next run; the others go on.

  Re-runs find nothing new. Hashes stay in `media_hashes`, so reuse is still caught, except for abandoned pending
  uploads, which were never evidence: their hash is dropped when they're claimed, so the kid may upload that photo
  again. Nothing sets `keep` yet; the favorite toggle comes with the review screens.
- **Uploads** (follow-ups from #75): a kid repeating their own upload that is still pending (an offline retry that
  lost its response) gets that photo back with 201 instead of 409. Repeating one that is past its 24 h replaces it:
  the old upload is claimed for retention and the new one is stored. A kid may hold 20 attachable pending uploads
  (`media.MaxPendingUploads`); more is 429 `rate_limited`, but a retry never is: the repeat is recognized before
  the cap, again if the cap check fails (a racing first upload), and atomically in the insert transaction. Parents
  aren't capped.

Built (#101, Android): the kid's chore detail has a Complete button that opens the evidence form: the checklist
steps, a note and the photos. Photos come only from the in-app CameraX camera (no gallery); the shutter fixes
`capturedAt` and a UUID `captureId`, and the JPEG stays in the app's private storage until the completion is
accepted. The camera permission is asked when the camera opens and a refusal leaves a retry button. A before/after
chore needs two photos (the first is sent as `before`, the others as `after`). Submit uploads each photo through
`POST /media/photos` (resending the same file after a lost response returns the pending photo, so a retry never
uses another pending slot), then sends `POST /instances/{id}/completion` with an `Idempotency-Key` that is reused
while the body is unchanged. Errors map to: offline or 5xx (retry as is), 429 on upload (too many pending photos,
wait), 409 `duplicate_media` / 413 / 415 (the photo is dropped, retake), 422 (a photo the server no longer accepts
is uploaded again), 409 `instance_closed` / `instance_not_open` (the list refreshes), and 422
`video_evidence_unsupported`. Resubmitting after a rejection is the same flow. The logic is in `:core`
(`Completion.kt`, `CompleteChore.kt`).

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
  12,000 px a side (413 `image_too_large`), and the memory decoding needs, estimated per format (progressive, RGB,
  CMYK and non-standard-sampling JPEG, 16-bit, interlaced and transparent-gray PNG, WebP with alpha or lossless
  coding cost more), must fit 96 MB (413 `image_too_complex`). A WebP frame may not be larger than the canvas its
  header declares. Animated WebP is 415. The photo is scaled to ≤1600 px (box averaging, so no large working buffers), turned
  upright from its EXIF orientation, and a ≤320 px thumbnail is made; both are re-encoded as lossy WebP holding
  pixels only (no EXIF, GPS, XMP or ICC). WebP encoding is pure Go (`github.com/gen2brain/vpx`, single-threaded,
  with decoder panics turned into 422 `invalid_image`), so the `CGO_ENABLED=0` build keeps working. One upload
  peaks at about 210 MB in the worst accepted case (87 MB for a 24 MP JPEG); `deploy/compose.yaml` suggests a
  `GOMEMLIMIT`.
- Built (#83): the JPEG estimate finds markers the way Go's decoder does. It skips stray bytes, stuffed `FF 00`
  pairs and `FF` fill bytes, reads on through the scan data to the end of image, and counts the last JFIF or Adobe
  marker. A file can no longer hide an Adobe RGB marker from the estimate and decode past the budget.
- SHA-256 is of the decoded pixels (dimensions, then RGBA rows), so re-saving a photo with other metadata doesn't
  change it; dHash is stored for later near-duplicate flagging. A kid's upload that matches `media_hashes` is 409
  `duplicate_media`, answered after decoding but before encoding; kid uploads are recorded there, which outlives
  the `media` row. Parents' uploads are neither checked nor recorded. (#27 returns a kid's own pending upload on
  a repeat instead; see above.)
- New media is `owner_kind = 'pending'` (no owner id) until a completion attaches it (#27). `GET /media/{id}` returns it with fresh
  signed links to the photo and thumbnail: parents see all media, a kid only pending media they uploaded, anyone
  else gets 404 `media_not_found`.
- Signed links (`GET /media/files/{key}?exp&sig`, public: the signature is the credential) last 10 minutes; a link
  claiming a later expiry is refused even if signed. The HMAC-SHA256 key is derived from a random secret created on
  first use in the `server_keys` table (row `media_url`), so **database backups contain it**: anyone holding a
  backup can mint links (they can also read the media files in the same data dir). Rotate it with
  `chorecast admin rotate-media-key` and restart the server; links handed out before stop working. A wrong
  or expired signature is 403 `invalid_media_url`; media whose row is gone, whose status isn't `ready` (retention is deleting or has deleted it), or whose file
  is missing is 410 `media_expired`. Files are served with `Cache-Control: private` and `nosniff`.
- Files live under `$CHORECAST_DATA_DIR/media/aa/bb/<key>` (`<key>_t` for the thumbnail), keyed by a random
  128-bit hex key and written to a temp file, then renamed. Photo processing runs one at a time; up to 4 uploads
  wait for it without reading their bodies into memory, and more get 503 `media_busy` with `Retry-After`; one
  caller may hold at most 2 of those places (more is 429 `rate_limited`). Multipart bodies over 8 KB are spooled to
  the temp dir before the handler runs (so the server needs a writable `/tmp`) and deleted after every request,
  whatever its outcome.

Built (#118, `internal/media`): `DELETE /media/photos/{id}` (scope `chores:complete`) lets the uploader discard
their own pending photo, so a kid who retakes photos can free pending slots (`MaxPendingUploads`) before the 24 h
`PendingTTL`. It claims the row like retention does (status `deleting`, hash dropped) in one transaction with a
conditional UPDATE on `owner_kind = 'pending' AND status = 'ready'`, so a completion attaching the same photo and a
discard can't both win. It then deletes the files and marks the row `expired`; a file that can't be deleted leaves the
row `deleting` for the retention job. The answer is 204. Another user's photo (a parent included) and an unknown id
are both 404 `media_not_found`; a photo attached to a submission is 409 `media_attached`. Repeating the call for a
pending photo already discarded or expired is 204 again.

## Review queue (parent)

- List of `submitted` instances, oldest first, grouped by kid, with thumbnails, AI verdict chip, checklist and note.
- Filters: kid, chore, "AI says done", "no media".
- Bulk select → approve. Bulk reject isn't offered, because rejection needs per-item feedback.
- Android notification for each submission (or batched if several) with **Approve** and **Reject** actions.
  Reject opens a quick-reason sheet. The actions call the API with the parent's app token from a background worker.

Built (#28, `internal/reviewqueue`, server only; the web screen is #34, the Android one #32). The AI verdict
chip, the "AI says done" filter and near-duplicate flags wait for Phase 3.

- `GET /review-queue` (`chores:review`, so parents only) is a flat, cursor-paged list of `submitted` instances,
  oldest submission first (ties by id). Clients group by `kidName` / `instance.completedBy`; a grouped response
  would make the cursor depend on the grouping. Each item has the instance (chore name, kid, due time,
  `completedAt`, `rejectionCount`, `lastRejection`), the chore's checklist `steps` with `done` for those the latest
  submission ticked, and that `submission` (note, `submittedAt`, photos with signed thumbnails or `expired`).
  Filters: `kidId` (a shared chore matches its members), `choreId`, `media=with|without` (the latest submission has
  photos or not), validated in the handler. A page takes a fixed number of queries.
- `POST /review-queue/approvals {instanceIds}` (1 to 100) approves each distinct id through `instances.Approve` in
  its own transaction and returns `results` in request order: `approved`, or a `code` such as
  `instance_not_submitted` or `instance_not_found`. Rejection stays single (`POST /instances/{id}/rejection`)
  because it needs feedback per chore; its `reason` quick picks are a fixed list (`not_done`, `not_finished`,
  `needs_redo`, `evidence_unclear`) validated by `instances.Reject`.

Built (#94, web admin). `/review` (`views/review/`) is a parent-only screen on `GET /review-queue`. The client
groups the flat list by kid (`review/queue.ts`) and shows each item's checklist with ticked steps, the note and a
thumbnail grid; a thumbnail opens the photo in a modal with arrow-key navigation, and expired photos show "Media
expired". Filters (kid, chore, with/without photos) reload the list, and "Load more" follows `nextCursor`. Items can
be approved or rejected one by one (a quick-reason select plus optional text, 422 avoided client-side), or selected
and approved in bulk through `POST /review-queue/approvals` with per-item failures listed; items that are approved,
or already decided (`already_approved`, `instance_not_submitted`, `instance_not_found`), leave the list. Signed photo
links expire after about 10 minutes, so the list reloads (keeping as many pages) when an image fails or after 8
minutes. The nav "Review" link carries a badge with the first page's count (up to 100) and a "+" when more exist.

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
