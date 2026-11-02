# Handoff: state of Phase 1 and how to keep going

Written 2026-11-02 for the next agent (or person) picking up Phase 1 (#2). Read this, then
`docs/architecture.md`, `docs/roadmap.md` and the feature spec named in the issue you're working on.

## Where things stand

The server is feature-complete for the v1 chore loop, and so is the web admin. Android has the kid, parent and
shared-tablet flows, reminders and the inbox. Issues are worked one PR each and PRs are squash-merged.
Agents may merge a PR when CI is green and review has passed. Coding subagents only commit; the orchestrating
session pushes and opens the PRs (see "Environment gotchas").

**Built** (each feature spec in `docs/features/` has a "Built (#N)" paragraph describing the package):

| Area | What | Where |
|---|---|---|
| Server | Setup wizard, parent auth/TOTP/OIDC, invites and admin CLI, household settings, kid profiles (bundled and uploaded photo avatars), device pairing and sessions, schedules, assignment modes, chore definitions and catalogs | #12-#22, #88 |
| Server | Rolling 7-day horizon (`internal/horizon`), day and week close (`internal/dayclose`), instance state machine (`internal/instances`), points ledger (`internal/ledger`), streaks (`internal/streaks`), auto-approve (`internal/autoapprove`) | #23-#26, #29, #30 |
| Server | Evidence (`internal/evidence`), media pipeline with duplicate detection, signed URLs and discard of a pending upload (`internal/media`), review queue (`internal/reviewqueue`) | #27, #28, #75, #118 |
| Server | Instances carry points, steps, description and evidence; kids' chore reads are scoped | #108, #116, #128 |
| Server | In-app notification inbox (`internal/inbox`, `/inbox`) | #98 |
| Server | Scheduled backups and `backup` / `restore` commands (`internal/backup`), owner-only audit log including backups and restores (`internal/audit`, `/audit`) | #36, #37, #121 |
| Docs | Install, first-run, SSO, backup and upgrade guides in `docs/guide/` | #38 |
| Web | Chore assignment editor, review queue, today screen with parent actions, kid ledger with adjustments and streaks, settings (points, streaks, approvals, media), phone-friendly nav, audit view | #93-#97 |
| Android | Pairing by QR or typed code, parent sign-in with TOTP and SSO, parent home (each kid's day), review queue, parent actions (adjust points, excuse, reassign, reverse an approval), devices list with revoke | #8, #103, #104 |
| Android | Kid today list and chore details, completion with CameraX photo evidence, discard of pending photos, kid history with evidence, unknown enum values decode instead of throwing | #100-#102, #118, #119 |
| Android | Inbox screen with unread counts, WorkManager sync and local reminders for a kid's own device, parent pairing QR | #99, #133 |
| Android | Shared tablet: profile picker with avatars, kid PIN pad, auto-lock, parent PIN elevation into a timed parent mode | #33 |

**Open work, Phase 1 issues from `gh issue list --state open`:**

1. **#31 (Android kid experience), #32 (Android parent experience), #18 (device pairing).** Their features are
   built; the issues stay open until the owner closes them. #18 is also open because nobody has run the
   end-to-end check (pair, pick a profile with a PIN, elevate as parent) on a real device or emulator.
2. **#99 and #35 (reminders without push).** The server inbox (#98), the Android inbox screen (#136), background
   sync with local reminders (#139) and the parent pairing QR (#140, closes #133) are all on `main`. Both
   issues stay open until the owner closes them. #35 owned offline capture-time skew and the offline grace for
   late completion; the Android code has no handling for either (no match for "skew" or "grace").
3. **#34 (web admin).** Every screen the issue names exists (chores with schedule editor and catalogs, kids,
   devices, parents and invites, review queue, ledger, settings, audit). The issue is open until the owner closes
   it. The chore form's video-evidence checkbox is present but disabled ("coming later"); video evidence is not built.
4. **#16 (kid profiles).** Avatars (bundled and uploaded), PIN, day end, quiet hours and school hours are stored
   and editable, and archiving revokes the kid's devices. The issue is open until the owner closes it.
5. **#2 (Phase 1 tracking), #1 (Phase 0 tracking), #65 (Renovate dashboard).**

**Open follow-ups:**

- **#138:** small tablet-mode fixes from the #137 review (a 403 from `GET /household` signs a parent out where
  only a 401 should, `clearSessionStores` leaves `parentStore` in state, `KidAvatar` never retries a failed
  download, and the idle lock uses `delay()`, which stops while the device sleeps). None is reachable today.
- **Release PRs #76 (server 0.3.0) and #114 (android 0.2.0)** need the owner to merge them.
- **Renovate PRs #80 and #81** fail the image jobs and `server-web` with `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH` on
  `pnpm install --frozen-lockfile`. The lockfile needs regenerating on those branches.
- **The parent inbox is not exposed in tablet parent mode.** Parent mode offers the overview, the review queue and
  a kid's page. The inbox is reachable from the parent app's home and from kid sessions, not from there.
- **School hours are stored and validated but not applied to reminders.** The kid's `quietHours` and
  `schoolHours` are on the kid object, and the server has no reader for either. `planReminders` (`:core`,
  `Reminders.kt`) uses `quietHours`, with a default 20:00 to 07:00 window for kids who have none, and ignores
  `schoolHours`. `docs/features/notifications.md` lists this as a known limit.

`docs/roadmap.md` checkboxes have **not** been ticked as things merged. Go by the issue and PR state, not the
boxes.

## Repo layout and stack

- `server/`: Go, chi + Huma v2 (OpenAPI 3.1), SQLite via modernc (no CGO), sqlc, goose. One binary
  (`cmd/chorecast`) serves the API, the embedded web admin (`internal/webui/dist`) and background jobs.
- `web/`: Vue 3 + Naive UI + Tailwind 4, vue-i18n (`src/locales/en.json`), an openapi-fetch client
  generated into `src/api/schema.d.ts`, Biome, Vitest (`src/__tests__/*.spec.ts`).
- `android/`: Kotlin/Compose, applicationId `dev.voidrot.chorecast`, `play`/`foss` flavors. `:core`
  holds API helpers and the generated Kotlin client built from `api/openapi-3.0.json`.
- `api/`: the exported OpenAPI specs (generated; commit them).
- `docs/features/*.md`: the accepted specs. Update the relevant one when you build something; most
  have a "Built (#N)" or implementation paragraph.

## Tooling: always go through mise

`mise.toml` pins the tool versions (Go 1.27, golangci-lint, sqlc, pnpm…). Tools on the plain `PATH` can
be older, so use `mise run …`. Commit with plain `git commit`: the lefthook hooks run with the mise shims on
`PATH` (see "Environment gotchas" for `mise exec`).

| Task | Command |
|---|---|
| Regenerate sqlc after editing SQL | `mise run gen:sql` |
| Export the OpenAPI specs | `mise run gen:openapi` |
| Regenerate the web client types | `mise run gen:web-client` (run after gen:openapi) |
| Server tests / lint | `cd server && go test -race ./...`, `mise run lint:server` |
| Web lint / typecheck / tests | `cd web && npx biome check src && pnpm typecheck && npx vitest run` |
| Build the web into the server embed dir | `mise run build:web` |

CI runs on PRs: `server-web` (gen drift, lint, tests), `android` (build, lint, unit tests), images,
and semgrep. **Generated files must be committed** or the drift check fails.

Commit messages follow Conventional Commits (a hook enforces it), e.g.
`feat(server): …`.

## Conventions that bite if you miss them

**Database**
- There's a single migration, `server/internal/db/migrations/00001_init.sql`, **edited in place** until
  v1 (no data to migrate yet). Add columns and tables there and regenerate sqlc.
- sqlc can't mix named (`sqlc.arg`) and positional (`?`) parameters in one query.

**Huma / API**
- Every operation declares `Security: auth.Require(scope…)` or `auth.Public()`. Operations allowed
  before setup also need `setup.AllowDuringSetup`.
- Errors are problem+json through `apierr.New(status, code, msg, details…)`. Field problems are
  `*huma.ErrorDetail` with `Location: "body.field[0]"`. Validation failures are 422
  `validation_failed`.
- **Schema names are global across packages.** Huma names schemas after the Go type, so give types
  specific names (`ScheduleRule`, `ChoreAssignment`, `DeviceSession`, `KidInput`…). A clash breaks the
  generated clients.
- Use `omitzero` (not `omitempty`) for struct-typed optional fields such as `caltime.Date`.
- Don't use enum-typed **query** parameters: the generated Kotlin client fails to compile. Validate in
  the handler instead.
- PUT replaces the whole object: optional fields left out are cleared. Keep web forms passing through
  fields they don't edit (see `assignment` in `web/src/chores/form.ts`).
- The service layer normalizes (trim, dedupe, drop irrelevant fields) and then validates. The API
  returns the normalized object.

**Time**
- All calendar maths goes through `internal/caltime`: `Household.At/DayClose/WeekStartOf/WeekClose`
  handle DST (a time in a gap resolves to the gap's end, a repeated time to the earlier instant) and
  `Date` handles civil dates.
- Inject `clock.Clock`; tests use `clock.NewFake`.

**Events and jobs**
- Domain events go through `events.Bus.InTx` / `tx.Emit`, in the same transaction as the change.
  `household.calendar_changed`, `chore.changed` and `kid.changed` trigger horizon regeneration, instance
  transitions emit `instance.*`, and the ledger emits `points.entry_posted`.
- `internal/jobs` has a SQLite-backed runner and a scheduler with `KidDayCloses`, `KidWeekCloses` and
  `HouseholdDayCloses` providers (per-kid effective day end). Wire new recurring work in
  `cmd/chorecast/background.go`. Delivery is at-least-once, so handlers must be idempotent.

**Web**
- Biome a11y: use `label-props { for }` with `input-props { id }`, and `aria-labelledby` on `NSwitch`
  and number inputs. Radio inputs sit inside their label with sr-only text.
- All UI text goes in `en.json`. Pure helpers get Vitest tests.

**Android**
- All strings live in resources. Lint forbids `LocalContext.getString` in composition; use
  `stringResource`.
- Contextual `OffsetDateTime`/`LocalDate` serializers are registered in `ApiClients.json`.
- When a server response gains a **required** field, update the Android test mocks (e.g.
  `HouseholdInfoTest`) or the `android` CI job fails.
- **Never auto-accept Android SDK licenses** locally; the owner refused that. CI has the SDK.
- The generated client sets `enumUnknownDefaultCase` (`android/api-client/build.gradle.kts`), so every generated
  enum has an `UNKNOWN_DEFAULT_OPEN_API` member. A `when` over a generated enum needs an `else`, and the unknown
  case must degrade gracefully (read-only, generic label), never throw.
- `:app` first compiles in CI, because there is no local SDK. `:core` is plain JVM and compiles and tests locally
  (`./gradlew :core:test`), so keep logic there. Read an `:app` change twice before pushing; the `android` CI job
  is the compiler.
- A trailing lambda binds to the last parameter, so adding a trailing parameter to a composable or constructor
  can silently capture an existing `{ … }` call site. Pass lambdas by name in constructors and composables that
  are likely to grow.

**Product constraints from the owner**
- The FCM relay must be the admin's own Firebase project. Don't ship a default, so we never handle
  kids' data.
- No model identifiers in commits, PRs or code.

## Decisions already made

**From the owner**
- Ledger reversals are exact negations, never clamped, and a reversal's `local_date` is the reversed entry's.
- The media signing key stays in the database (`server_keys` row `media_url`), so backups contain it.
  `chorecast admin rotate-media-key` rotates it. Signed link expiry is capped at 10 minutes.
- Default streak milestones are 3 days / 5 points, 7 / 15 and 30 / 50 (the `households.streak_milestones` default).
- The FCM relay must be the admin's own Firebase project, with no shipped default, so we never handle kids' data.
- No model identifiers in commits, PRs or code, and no trailers.
- Never auto-accept Android SDK licenses locally. CI has the SDK.

**Behaviors fixed in the feature specs**
- Completing after the instance's close is 409 `instance_closed`. Reopening after close goes straight to missed
  (`dayclose.MissInTx`). Reassigning onto a closed day is 409.
- Re-approval: single-kid and `completer_takes_all` credit the current earner; `split_evenly` restores the first
  credits without re-splitting.
- A video-evidence chore can't be completed until video exists (422 `video_evidence_unsupported`).
- A kid reads only their own evidence submissions.
- Archived kids' days still close. "Skip archived kids" applies to assignment only.
- The shared-chore "group" is the chore's own kid list; there is no group entity.

## Environment gotchas

- **The push trap.** Worktree branches made by the agent tool track `origin/main`, and `push.default` is
  `upstream`, so a plain `git push` goes to **main**. Run `git branch --unset-upstream` on every new branch and
  push with an explicit refspec: `git push origin HEAD:refs/heads/<branch>`.
- **Subagent pushes hang** in this environment. The lead pushes and opens the PRs; subagents commit only.
- **`mise exec -- git commit` is blocked** in the agent sandbox. Plain `git commit` works, and lefthook finds the
  mise-pinned tools through the shims on `PATH`.
- **Hooks.** A global `core.hooksPath` once disabled lefthook; the owner removed it. If hooks don't run, check
  `git config core.hooksPath` and run `mise run setup` to reinstall them.
- **Media memory.** The worst accepted upload peaks near 210 MB. `deploy/compose.yaml` suggests a 512m memory
  limit and `GOMEMLIMIT=400MiB`. The photo size cap is `CHORECAST_MEDIA_MAX_PHOTO_MB`.

## What the next client work can build on

The API that the Android and web clients call exists, and the specs in `api/` cover everything merged. Paths are under `/api/v1`.

- **Instances and transitions:** `GET /instances`, then `POST /instances/{instance}/completion`, `approval`,
  `approval/reversal` (unapprove), `rejection`, `excusal` and `reassignment`. Conflicts carry codes such as
  `instance_not_open`, `instance_planned` and `instance_closed`.
- **Evidence:** `completion` takes the checklist, note and photos. `GET /instances/{instance}/evidence` returns
  every submission, oldest first, with signed photo links.
- **Media upload:** `POST /media/photos` (multipart `file`), then reference the returned `mediaId` in the
  completion. A kid's photo also sends `capturedAt` and `captureId`. Files are served from `/media/files/{key}`
  by signed link.
- **Review queue:** `GET /review-queue` (submitted chores, oldest first, filters `kidId`, `choreId`, `media`) and
  `POST /review-queue/approvals` (bulk approve, per-id results). Reject one at a time with feedback.
- **Inbox:** `GET /inbox`, `POST /inbox/read-all` and `POST /inbox/{id}/read`. No display text is stored; the
  client renders from `kind` and `data`.
- **Discard a pending photo:** `DELETE /media/photos/{id}` removes the caller's own upload before it is attached.
- **Devices:** `POST /devices/pair`, `/devices/session`, `/devices/elevate`, `GET /devices/picker`, and for parents
  `GET /devices`, `POST /devices/pairing` (create a code), `POST /devices/{id}/lock` and `DELETE /devices/{id}`.
- **Audit:** `GET /audit` (owner only).
- **Points:** `GET /kids/{kid}/points` and `/points/entries`, `POST /kids/{kid}/points/adjustments` and
  `/kids/{kid}/points/entries/{entry}/reversal`.
- **Streaks:** `GET /kids/{kid}/streaks`.
- **Idempotency applies to JSON POSTs only.** A client that wants retry safety on the body-less endpoints
  (approval, completion without evidence) must send `Content-Type: application/json` with the body `{}` and an
  `Idempotency-Key`. PR #91 fixed the panic that keyed POSTs hit before.
- The Kotlin client is generated from `api/openapi-3.0.json` at Gradle build time. The web types come from
  `api/openapi.json`.

## Local manual testing

Start a throwaway server on a fresh data dir and run setup through the API:

```sh
mise run build:web && (cd server && go build -o /tmp/chorecast ./cmd/chorecast)
CHORECAST_ADDR=127.0.0.1:8080 CHORECAST_DATA_DIR=/tmp/cc-data /tmp/chorecast serve
# the setup token is printed in the log: "Setup required: open …/setup?token=…"
curl -X POST localhost:8080/api/v1/setup -H 'content-type: application/json' -d '{"token":"…",
  "household":{"name":"Test","timezone":"America/Chicago","dayEnd":"21:00","weekStart":"sunday"},
  "owner":{"displayName":"Mom","username":"mom","password":"correct horse"},
  "kids":[{"displayName":"Sam","color":"#2563eb"}]}'
```

Use a **new** data dir after schema changes (the migration is edited in place). Playwright and Chromium
are preinstalled in the cloud container (`require('playwright')`) for browser checks. In API tests,
`newAuthServer(t)` gives a set-up household (America/Chicago, day end 21:00, weeks start Sunday,
fake clock at 2026-10-31 12:00Z) with owner `mom` / `correct horse battery`.

The Dex OIDC dev setup is in `deploy/dev/compose.oidc.yaml`. In the cloud container dockerd has to be
started by hand and Dex run with `--network host`.
