# Roadmap

Status: draft (2026-10-30). Sequenced from the specs in [features/](features/) and [architecture.md](architecture.md).
Each phase ends in something a family can actually use; each checkbox is roughly one issue/PR.

| Phase | Theme | Outcome |
|---|---|---|
| 0 | Foundations | Skeleton that enforces the cross-cutting rules (scopes, time, jobs) so features don't retrofit them |
| 1 | **v1: core chore loop** | Parents set up chores, kids complete them with a photo on Android, parents approve, points add up |
| 2 | Rewards & behavior | Points become spendable; behavior tracking, streaks, bonus chores, schedule disruptions |
| 3 | Media, notifications & AI | Real push, rich evidence (video, S3, dedupe, annotations), offline kids' devices, AI coaching |
| 4 | Reporting & polish | Dashboards, history, badges, family wall display, printable charts, exports |
| 5 | Integrations & distribution | API keys, timed entitlements (PC game time), MQTT, CLI; Helm chart; Google Play |

v1 = end of Phase 1, released as a container image + GitHub Release APK.

---

## Phase 0: Foundations

Goal: the rules every later feature depends on exist and are tested before any feature code lands.

- [ ] **Clock & household time**: `Clock` interface (real + fake for tests); helpers for household timezone, effective
      day end per kid, local date, week start, week/day close instants. All scheduling code takes a clock.
- [ ] **Job runner**: SQLite-backed `jobs` table (scheduled + queued), single-process worker with per-queue concurrency,
      retries/backoff, idempotency keys; scheduler for day close / week close / horizon / maintenance.
- [ ] **Domain event outbox**: events written in the same transaction as state changes; dispatcher to in-process handlers.
- [ ] **Auth core**: opaque token store (hash, kinds incl. `display`/`api`), argon2id helper, principal resolution
      (cookie or bearer), **scope-aware Huma middleware**; every operation declares scopes.
- [ ] **Error & API conventions**: problem+json, pagination (cursor), idempotency-key support, `apiVersion` check.
- [ ] **Android foundations**: generated Kotlin client from `api/openapi.json` (`:api-client` module), DI, navigation
      shell, secure token storage (Keystore), server-URL handling, version-compat check.
- [ ] **Web foundations**: auth-aware router, layout shell, generated client usage patterns, i18n (`vue-i18n`) wiring.
- [ ] **Android i18n + strings discipline** (all UI text in resources).
- [ ] **CI green on all three apps** (Android SDK in CI), generated-code drift checks.
- [ ] **Release plumbing**: APK signing via secrets, semver `versionCode` (done), image tags `:<v>` and `:<v>-media`.

Exit: a fake-clock test can advance across a day close in another timezone and observe a scheduled job run; an
endpoint without declared scopes fails CI.

## Phase 1: v1, the core chore loop

Goal: one household runs its daily chores on Chorecast end to end.

**Setup & people** ([household.md](features/household.md))
- [ ] First-run token + web setup wizard (household, first owner, kids, starter content)
- [ ] Parent login (password + TOTP + recovery codes), web cookie sessions, app tokens
- [ ] Optional OIDC login (web + server-brokered app flow)
- [ ] Invite links; owner/parent roles; `admin reset-password` / `disable-2fa` CLI
- [ ] Kid profiles (nickname, avatar from built-in set, color, birth date, PIN, day-end override, slug)
- [ ] Household settings page (timezone, day end, week start, negative balance, visibility, retention, auto-approve)

**Devices** ([architecture.md](architecture.md#flows))
- [ ] QR pairing (kid-bound and shared); profile picker with PIN; parent PIN elevation
- [ ] Devices page: list, revoke, remote lock to picker

**Chores & scheduling** ([schedules.md](features/schedules.md))
- [ ] Chore definitions: points, description, steps/checklist, slots, estimated minutes, min age, penalty,
      requires approval, evidence requirements, escalate flag, location/equipment (simple)
- [ ] Schedule rule model + validation + "next occurrences" preview (all patterns incl. flexible weekly)
- [ ] Assignment modes: fixed, everyone, rotation (skip-and-owe), auto (minutes-based), shared (completer/split)
- [ ] Rolling 7-day horizon generator; regenerate untouched instances on edit; "planned" vs today
- [ ] Day close (per kid) and week close jobs: missed, penalties, flexible leftovers

**Workflow & points** ([points.md](features/points.md), [approvals-evidence.md](features/approvals-evidence.md))
- [ ] Instance state machine: open → submitted → approved / rejected→open, missed, excused; reassign
- [ ] Points ledger: chore credit, penalty, reversal, manual adjustment; balances; clamp when negatives disallowed
- [ ] Evidence v1: checklist ticks, notes, **photos** (in-app camera only, local disk, WebP + thumbnail, EXIF strip,
      signed URLs, capture-window check, exact-duplicate rejection), retention job
- [ ] Review queue (web + app): approve, bulk approve, reject with text feedback
- [ ] Auto-approve timeout (household default + per-chore override)
- [ ] Daily streak + per-chore streak computation (milestone bonuses)

**Apps**
- [ ] Android kid: today list, chore details/steps, complete with photo, points + streak, history with past media
- [ ] Android parent: home (kids overview), review queue, approve/reject, adjust points, mark excused/reassign, pair device
- [ ] Android shared tablet: profile picker, auto-lock to picker
- [ ] Web admin: chores CRUD with schedule editor, kids, devices, review queue, per-kid ledger, settings
- [ ] Reminders without push: WorkManager polling + locally scheduled reminder notifications; in-app inbox

**Operations**
- [ ] Scheduled backups + `backup` / `restore` CLI
- [ ] Audit log (writes + owner view)
- [ ] Docs: install (compose), first run, backups, upgrading

Exit: two weeks of real household use without manual DB fixes; restore from backup verified.

## Phase 2: Rewards & behavior

- [ ] Rewards catalog: per-kid availability, limits/cooldowns, stock, needs-delivery step
- [ ] Redemptions with holds; approve/deny; to-deliver list; savings goals with ETA
- [ ] Transfers between kids (parent-approved)
- [ ] Recurring adjustments (weekly allowance)
- [ ] Behavior presets + per-kid overrides; quick log (multi-kid); Android widget
- [ ] Kid self-report and sibling nominations (rate-limited) into the review queue
- [ ] Way back (per-preset suggestion, linked bonus chore/preset)
- [ ] Behavior goals, daily check-in scorecard, behavior streaks
- [ ] Bonus chores (claimable pool)
- [ ] Absences and household pause; swaps (30 min expiry, parent resubmission)
- [ ] Routines (display groups with windows)
- [ ] Leaderboard (weekly earned) when enabled; behavior trend chart

Exit: kids can earn, see and spend points; parents log behavior in ≤3 taps.

## Phase 3: Media, notifications & AI

**Notifications** ([notifications.md](features/notifications.md))
- [ ] Notification planner: recipients, categories × channel prefs, quiet hours (incl. school hours + no-school dates), batching
- [ ] UnifiedPush (content-free, RFC 8291), Android notification channels, approve/reject actions from notification
- [ ] FCM via admin's own Firebase project (**spike first**: runtime `FirebaseOptions`)
- [ ] Web push (VAPID) for the admin; email (SMTP); webhooks (signed, event catalog defined with the API)
- [ ] Daily and weekly parent summaries; escalation for flagged chores

**Media** ([approvals-evidence.md](features/approvals-evidence.md))
- [ ] Video evidence: ffmpeg detection, 720p transcode queue (concurrency 1), poster frames; `-media` image
- [ ] S3-compatible storage backend + presigned URLs
- [ ] Near-duplicate detection (dHash) with flagging + side-by-side review
- [ ] Rejection annotations (draw + text) in app and web
- [ ] Offline outbox with resumable uploads, device-time acceptance rules, clock-skew flags
- [ ] Kiosk mode for shared tablets

**AI** ([points.md](features/points.md#ai-evidence-check))
- [ ] Provider interface (OpenAI-compatible + Anthropic), settings, privacy disclosure
- [ ] Photo coaching for kids; verdicts in review queue; `not_yet` blocks auto-approve
- [ ] Reference photos + rubric per chore; video frame sampling
- [ ] AI way-back drafts for negative behaviors (separate toggle)

Exit: a household on ntfy or its own Firebase project gets timely, content-free pushes; a kid's offline tablet syncs without loss.

## Phase 4: Reporting & polish

- [ ] Per-kid dashboard, chore insights, fairness view, points flow
- [ ] Calendar, activity feed, per-chore history; CSV export per report
- [ ] Kid "My week" and achievements shelf; built-in + custom badges
- [ ] Family dashboard display mode (`display` tokens, SSE)
- [ ] Printable weekly chart
- [ ] AI weekly insight (anonymized)
- [ ] Household/kid data export; archive and hard delete flows
- [ ] Accessibility pass (TalkBack, contrast), UX polish, translation readiness check

## Phase 5: Integrations & distribution

- [ ] API keys UI + scopes + kid restriction ([integrations.md](features/integrations.md))
- [ ] Timed entitlements: types, grants, sessions, windows, heartbeats, expiry
- [ ] Integration-initiated redemption + `auto_approve_from_integrations`
- [ ] MQTT publisher + Home Assistant discovery
- [ ] `chorecast api` CLI (generated client), cross-platform release binaries
- [ ] Integrations guide with Windows PC lock/unlock walkthrough
- [ ] Helm chart (`deploy/helm`)
- [ ] Google Play: Families policy review, data-safety form, privacy policy, Play listing, `play` flavor release

## Cross-cutting, every phase

- Tests: domain logic with the fake clock (DST transitions, per-kid day end, month-end rules); API tests via Huma;
  Android unit tests for view models; a small Playwright smoke test for the web admin.
- Keep `api/openapi.json` and generated clients in sync (CI drift check).
- Migrations are forward-only once v1 ships; pre-v1, `00001_init.sql` may still be edited.
- Performance budget: server idle < 50 MB RSS on a Raspberry Pi 4; p95 API < 100 ms for household-scale data.

## Risks to retire early

| Risk | Mitigation / when |
|---|---|
| FCM with the admin's own Firebase project may not work from the Play-signed app | Spike at the start of Phase 3; fallback is UnifiedPush + polling only |
| Timezone/DST bugs in day close and schedules | Phase 0 clock abstraction + table-driven tests across zones |
| Android is new territory for you | Keep the app conventional (Compose + ViewModel + generated client); build kid "today" screen first as the learning slice |
| Pi performance for media | Image processing inline is light; video only with ffmpeg + concurrency 1; measure on a Pi in Phase 3 |
| Scope creep before v1 | Phase 1 list is the v1 contract; everything else waits |
