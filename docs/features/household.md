# Household & people management

Status: accepted (planning session, 2026-10-30). Auth mechanics live in [../architecture.md](../architecture.md).

## Decisions

| Topic | Decision |
|---|---|
| First run | **Web setup wizard gated by a one-time token** printed in the server log. |
| Parent roles | **Owner(s) + parents.** Parents manage everything household-level; only owners manage server-level settings. |
| Adding parents | **Invite link/QR**, single use, expires in 7 days; invitee sets a password or links SSO. |
| Account recovery | Another **owner resets** password/2FA; **recovery codes** with 2FA; **server CLI** `chorecast admin reset-password`; **email reset** when SMTP is configured. |
| Kid profile | Display name/nickname only (no surname), **avatar + color**, **birth date**, kid settings: PIN required, quiet hours, school hours, **day-end override**. |
| Kid removal | **Archive** (hide, revoke devices, stop scheduling, keep history) or **hard delete** (erase kid, media, entries, evidence). |
| Admin split | **Web admin has everything**; parent app has daily operations plus simple chore edits; server-level settings are **web only**. |
| Devices | **List + revoke**, **remote lock to picker**, **stale device alerts**. No rename or reassign; re-pair instead. |
| Shared tablet | **Optional kiosk mode** per device (Android lock-task/screen pinning; exit needs a parent PIN) with optional always-on hours. |
| Backups | **Scheduled backups** and a **backup/restore CLI**. No web download. |
| Data export | Per-kid or household **JSON/CSV export**. |
| i18n | **i18n-ready, English only** at launch. |
| Audit log | **Yes, owner-visible**, retained 1 year. |

## First run

1. On start with an empty database, the server generates a setup token and logs
   `Setup required: open http://<host>:8080/setup?token=<token>`. Every other API returns `503 setup_required`.
   The token is single-use, stored hashed, and regenerated on each restart until setup completes.
2. Wizard steps (web):
   1. **Household**: name, timezone (browser-detected default), day end (default 00:00), week start (Monday).
   2. **First owner**: display name, username, password (+ optional TOTP); or "sign in with SSO" if OIDC env is set.
   3. **Kids** (skippable): nickname, birth date, avatar/color, PIN on/off.
   4. **Starter content** (skippable): example chores, behavior presets, rewards. All editable and deletable.
   5. **Connect devices**: shows a pairing QR and a link to the Android app (GitHub Releases / Play).
3. Completing the wizard creates everything in one transaction and invalidates the token.

Built so far (#12): household, owner (password), kids (name, birth date, color, PIN on/off), and an app download
link. The remaining parts arrive with their features: TOTP enrolment in the owner step (#13), SSO owner (#14),
kid avatars (#16), the pairing QR (#18), and starter chores (#20; behavior presets and rewards in Phase 2).

## Roles & permissions

| Capability | Owner | Parent | Kid |
|---|---|---|---|
| Daily ops: approve/reject, log behavior, rewards, adjustments, mark away, pair device | ✓ | ✓ | — |
| Manage chores, routines, presets, rewards, goals, check-ins | ✓ | ✓ | — |
| Manage kids (create, edit, archive) and household settings (timezone, day end, retention, visibility, auto-approve…) | ✓ | ✓ | — |
| Invite parents; reset another parent's password/2FA | ✓ | — | — |
| Promote/demote owners (≥1 owner must remain) | ✓ | — | — |
| **Hard delete** a kid | ✓ | — | — |
| Server-level: SSO, storage/S3, AI provider, email/SMTP, FCM project, webhooks, backups, audit log | ✓ | — | — |
| Remove a parent | ✓ | — | — |

Server-level settings that also exist as env vars: **env wins** and the UI shows them read-only ("set by environment").

Built (#17, `internal/household`): `GET/PUT /household` (read: any household principal; write: parents) with
name, timezone, day end, week start, `allowNegativeBalance` (default off), `siblingVisibility`
(`private`|`leaderboard`, default private), `mediaRetentionDays` (default 30), `autoApproveHours` (absent = never,
the default) and `offlineGraceMinutes` (default 120), and `defaultChoreMinutes` (default 10: the fair auto-pick's load for a chore
without an estimate). The day end can't move earlier than a kid's override.
Changing the calendar emits `household.calendar_changed` in the same transaction; the horizon generator (#23)
subscribes to it. `GET /server/settings` (owners) lists env-backed settings with `source: environment|default`;
secrets are never returned. Swaps and nomination limits join the settings with their features.
`streakMilestones` (#30) is a list of `{days, points}` (at most 20, days 1–3650 and distinct, points 1–100000),
returned sorted by days. A new household starts with 3/5, 7/15 and 30/50; leaving the field out of a PUT clears it
(no streak bonuses), like the other optional fields. Streaks are in points.md.

## Parents

- **Invites** (`internal/parents`): `invites(token_hash, role, created_by, expires_at, used_at, used_by)`. Shown
  once as link + QR (`/invite?token=…`; the page moves the token to sessionStorage). Invitee gives a display
  name and username/password, or joins with SSO (`/auth/oidc/start?client=invite`; username optional, no
  password until they set one on the Account page). Owners can revoke unused invites; expired ones are
  purged by daily maintenance.
- **Owner-only** API (scope `parents:manage`): invites, `PUT /parents/{id}/role`, `DELETE /parents/{id}`,
  `POST /parents/{id}/reset-password` (returns a temporary password once, signs them out everywhere),
  `POST /parents/{id}/disable-totp`. Demoting or removing the last owner answers 409 `last_owner`.
  Removing keeps the row (`removed_at`) so history still names them, and frees the username.
- **2FA**: TOTP optional per parent; enabling it shows 10 one-time **recovery codes** (stored hashed).
- **Recovery** order: recovery code → another owner resets → email (if SMTP, Phase 3) → CLI on the host
  (`docker exec chorecast /chorecast admin reset-password <username>` prints a temporary password;
  `admin disable-2fa <username>`). The CLI is safe to run while the server is running.
- **Removing a parent** revokes their sessions and app tokens. Their name stays on historical entries.
- **Parent PIN** for shared-tablet elevation (behavior.md); set in profile, separate from the password.

## Kids

```
users (role = child): display_name, avatar (built-in id or media id), color, birth_date,
                      pin_hash?, pin_required, day_end_override?, quiet_hours, school_hours, archived_at
```

- **Built-in avatar set**: 24 DiceBear **Pixel Art** avatars (CC0), generated once by `mise run gen:avatars`
  into `web/public/avatars/pixel-NN.svg` and served by the server at `/avatars/<id>.svg` (the Android app loads
  them from there, so there's one copy). Licence in `THIRD_PARTY_NOTICES.md`. First-run setup assigns one to each
  kid. An optional **uploaded photo** (processed like evidence, never expires) comes with the media pipeline (#27).
- **API** (`internal/kids`): `GET /kids` (`?archived=true` for parents), `GET/PUT /kids/{id|slug}`, `POST /kids`,
  `PUT /kids/{kid}/pin`, `POST /kids/{kid}/archive|unarchive`. Editing replaces the profile; optional fields left
  out are cleared. The slug stays unless a new one is given (changing it breaks old links/scripts).
- **Day-end override**: a kid's day can end **at or before** the household day end (e.g. 19:30 for a 6-year-old).
  Everything that says "day close" for a kid (missed, streaks, reminders, offline grace) uses the kid's
  **effective day end**. Household-level jobs (daily summary, horizon generation) run at the household day end,
  after every kid's day has closed.
- **School hours**: weekday time window used as an extra quiet window for that kid's notifications.
- **Archive**: sets `archived_at`, revokes the kid's devices and removes them from shared-device pickers, deletes
  untouched future instances, removes them from rotations (rotation cursors skip them), and keeps all history.
  Unarchive restores scheduling.
- **Hard delete** (owner only, typed confirmation): deletes the kid row and cascades to instances, evidence and media
  (including storage objects), ledger entries, behavior events, goals, check-ins, redemptions, and notifications. Audit-log
  entries about the deletion keep only "kid #id deleted by X". Irreversible; offered with an export first.

## Devices

- **Devices page** (web + parent app): name (from the app's device model, e.g. "Pixel Tablet"), kind
  (kid-bound, shared + which kids, or **display** for the family dashboard; see reporting.md), app version, last seen, push transports, kiosk on/off. **Revoke** deletes
  the token immediately; the app returns to the pairing screen on next contact.
- **Remote lock to picker**: sends a push (or applied on next sync) that ends the active kid or parent session
  on a shared tablet.
- To change who a device belongs to, revoke it and pair again (no reassign by design).
- Built (#19): `GET /devices` (last seen = latest request from the device or its sessions; the open session's user),
  `DELETE /devices/{id}` (revoke: deletes the device row, its token and sessions), `POST /devices/{id}/lock`
  (ends sessions). Until push (Phase 3) the app checks in every 30 s while a session is open and every 60 s at
  the picker, so a remote lock or revoke applies within about a minute. The app reports its version with each
  picker request. Parents get the list in the web admin and the parent app.
- **Kiosk mode** (shared devices): parent enables it on the device itself after PIN elevation. The app calls
  `startLockTask()` (screen pinning; no device-owner setup needed). Leaving requires parent PIN elevation.
  Optional "keep screen on" schedule (e.g. 06:30–20:30), applied via a window flag while in the foreground.

## Settings inventory

| Scope | Where | Examples |
|---|---|---|
| Household | web + app (basic) | name, timezone, day end, week start, allow negative balance, sibling visibility, retention days, auto-approve timeout, offline grace, default chore minutes, swaps need approval, nomination limit, streak milestones |
| Server | web (owner) / env | OIDC, storage/S3, AI provider + toggles, SMTP, FCM project, webhooks, backup schedule and retention, media limits |
| Per kid | web + app | PIN, day-end override, quiet/school hours, notification prefs, preset overrides |
| Per parent | web + app | profile, password, TOTP, parent PIN, notification prefs, quiet hours |

## Backups & export

- **Scheduled backups** (owner config; default daily at household day end + 1 h, keep 7): consistent DB snapshot
  via `VACUUM INTO`, plus local media, into `$DATA_DIR/backups/chorecast-<timestamp>.tar.gz` with a manifest
  (version, schema version, counts). Optional upload to an S3 bucket. Failures raise a `problems` notification.
  With S3 media storage, backups include the DB only and the docs say media is the bucket's responsibility.
- **CLI**: `chorecast backup [--out file]`, `chorecast restore <file>` (refuses to run while the server is running
  against the same data dir; checks schema version, runs migrations after restore).
- **No web download** of backups, by decision. Backups contain every kid's data and photos, and every secret in
  the database: the media signing key (`server_keys` row `media_url`), parents' TOTP secrets, password and token
  hashes. Anyone holding a backup can forge media links and generate TOTP codes, so keep backups as private as
  the data dir. After a leak, rotate with `chorecast admin rotate-media-key` and re-enroll TOTP.
- **Export** (**any parent**; audit-logged): household or single kid, as a zip of JSON (complete) plus CSVs (ledger, instances,
  behavior). Media is optional. Generated as a background job, available for 24 h, then deleted.

Built (#36): the server takes a backup daily at the household day end plus `CHORECAST_BACKUP_AFTER_DAY_END`
(default `1h`, any Go duration under 24 h) and keeps the newest `CHORECAST_BACKUP_KEEP` (default 7) in
`$DATA_DIR/backups`. A `CHORECAST_BACKUP_KEEP` under 1 (or not a number) falls back to 7 with a warning in the
log; there is no setting that turns scheduled backups off. These are server settings from the environment;
there is no backup UI. The job (`backup.scheduled`, its own queue) names each file after its scheduled time, so
a retried run finds its own file and leaves it unchanged, and it skips runs more than 24 h late (caught up after
downtime) because the newest one covers them. Pruning counts every `chorecast-<timestamp>.tar.gz` in that dir,
CLI backups written there included. The snapshot runs on a read-only connection of its own, so it doesn't block
the server's single connection. In WAL mode `VACUUM INTO` is a reader: on a 108 MB database it took about 0.1 s,
and concurrent writes stayed under 1 ms. The archive holds `chorecast.db`, then `media/aa/bb/<key>` streamed from
disk, then `manifest.json` (format, app version and commit, creation time, goose schema version, row count per
table, media file count and bytes). It is written owner-only (0600) to a temp file next to the target and
hard-linked into place, so an existing file is never replaced. Work dirs a crashed backup left (`.chorecast-backup-*`)
are removed by the next backup once they are an hour old. A failed backup is logged, and the job makes up to 3
attempts. The `problems` notification arrives with notifications. S3 is not built yet.

`chorecast backup [--out PATH]` writes the same archive, by default into `$DATA_DIR/backups`, and is safe while
the server runs. `chorecast restore [--force] <file>` restores into `CHORECAST_DATA_DIR`. **Restore only archives
you created**: an archive replaces every account, secret and photo.
- Locks (`flock`, released by the kernel even after a crash): the server holds `chorecast.lock` exclusively, so
  restore refuses while a server uses the data dir, and a second server is refused too. Restore also holds
  `chorecast-restore.lock` exclusively, which `chorecast admin` and `chorecast backup` take shared: they work beside
  a running server but not during a restore, and a restore waits for them. In Docker, stop the service and restore
  with `docker compose run --rm chorecast restore /data/backups/<file>`.
- It refuses a data dir that already has a database or media, unless `--force`, and one whose `chorecast.db` or
  `media` is a symlink or a mount point (the swap would replace the link with plain files).
- It extracts into a staging dir (`.restore-*` in the data dir) and accepts only regular files named
  `manifest.json`, `chorecast.db` or `media/aa/bb/<key>` in the storage layout. Any other path, absolute paths,
  `..`, symlinks, hard links, sparse files, directories and duplicate entries are refused, and nothing is written
  outside the staging dir. Extraction stops at a budget: the free space on the data disk minus 256 MiB, with
  1 GiB at most per media file. It copies in chunks and stops on Ctrl-C.
- It checks the gzip checksum, the database integrity (`PRAGMA quick_check`), and that the row counts and media
  totals match the manifest.
- A schema newer than this binary's is refused. An older one is migrated in staging with the embedded
  migrations. Until v1 the single migration is edited in place, so two builds can both report schema 1 with
  different tables; the check only protects across releases once migrations are forward-only.
- Only then does it write the marker `chorecast.restoring` (naming where the previous data goes), move the old
  database (with its `-wal`/`-shm`) and media aside into the staging dir, move the restored ones in, and remove
  the marker and the staging dir. A restore refused before this step leaves the data dir untouched. If a move
  fails, the moves are undone and the data dir is as it was. If undoing fails too, or the process dies mid-swap,
  the marker and the staging dir stay: `serve`, `restore`, `backup` and `admin` then refuse to start and print
  where the previous data is. Move it back (or keep the restored data), then delete the marker. The next restore
  removes leftover `.restore-*` dirs once no marker exists. The `backups` dir is never touched.

## Audit log

`audit_log(id, actor_id, actor_kind: parent|system|cli, action, target_kind, target_id, summary, data, created_at)`

Covers settings changes, approvals/rejections, ledger adjustments and reversals, pairing/revocation, kiosk
toggles, invites, role changes, password/2FA resets, archive/delete, backups/restores, exports. Owner-visible in
the web admin with filters; purged after 365 days by the daily maintenance job. The points ledger stays the source
of truth for points; the audit log records who did what around it.

## i18n

- Android: all UI strings in `strings.xml`, plurals via `plurals`; dates and numbers via the locale.
- Web: `vue-i18n` with `en` messages; dates via `Intl`.
- Server: user-facing text the server produces (notification titles/bodies, emails, AI prompts) goes through a
  small Go message catalog keyed by event type. Locale per user (default household locale `en`).
- Translations later via Weblate or similar; not in v1 scope.

## Open questions

- None outstanding.
