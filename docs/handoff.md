# Handoff: state of Phase 1 and how to keep going

Written 2026-11-02 for the next agent (or person) picking up Phase 1 (#2). Read this, then
`docs/architecture.md`, `docs/roadmap.md` and the feature spec named in the issue you're working on.

## Where things stand

The server side of the chore loop is built. What is left is the clients, plus reminders, backups, the audit
log and the install docs. Issues are worked one PR each and PRs are squash-merged. This session's arrangement
was that agents may merge a PR when CI is green and review has passed. Pushing and opening PRs still goes
through the owner (see "Environment gotchas").

**Merged** (each feature spec in `docs/features/` has a "Built (#N)" paragraph describing the package):

| Issue | What | PR |
|---|---|---|
| #12-#15, #17, #19, #21, #22 | Setup wizard, parent auth/TOTP/OIDC, invites and admin CLI, household settings, devices page, schedules, assignment modes | #46, #54, #56, #57 and earlier |
| #20 | Chore definitions and catalogs (issue closed) | #55 |
| #23 | Rolling 7-day horizon generator (`internal/horizon`) | #77 |
| #75 | Media pipeline: photo upload, duplicate detection, signed URLs (split out of #27) | #78 |
| #26 | Points ledger (`internal/ledger`; issue closed). The first commit, cf2910b, was pushed straight to main by mistake with no PR, and the owner chose to keep it | cf2910b, #79 |
| #24 | Day and week close (`internal/dayclose`) | #82 |
| #25 | Instance state machine (`internal/instances`) | #84 |
| #30 | Streaks with milestone bonuses (`internal/streaks`) | #85 |
| #29 | Auto-approve (`internal/autoapprove`) | #86 |
| #27 | Chore evidence (`internal/evidence`) | #87 |
| (fix) | Keyed POSTs no longer panic after their handler ran | #91 |

**In review:** #28, the review queue (server only: list submitted chores, approve in bulk), PR #90. Check its
state before you build on it.

**Still open, in the order to take them:**

1. **#31, #32, #33: Android** (kid experience, parent experience, shared tablet mode).
2. **#34: web admin.** Screens for chores, kids, devices, review queue, ledger and settings. It also owns the
   chore **assignment editor** (the chore form only passes the assignment through today), the review-queue and
   ledger screens, and the video-evidence checkbox.
3. **#35: reminders without push** (polling sync, local reminders, in-app inbox). It owns offline
   capture-time skew and the offline grace for late completion.
4. **#36: backups.** A database backup contains the media signing key (see "Decisions already made").
5. **#37: audit log.** The `instance.*` and `points.entry_posted` events are the hooks.
6. **#38: install, first-run, backup and upgrade docs.**
7. Follow-ups: **#83** (media: the JPEG header scan should skip stray bytes like Go's decoder; minor), **#88**
   (kid photo avatars, split from #16) and **#89** (make the per-submission photo limit configurable through
   `CHORECAST_MEDIA_MAX_FILES`).

#16 (kid profiles; the remaining work is #88) and #18 (device pairing; open only because nobody has run the
end-to-end check on a real device) are also still open. The bodies of #26, #27 and #28 are scoped to the
server; their UI lives in #31, #32 and #34.

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

The API that Android (#31-#33) and web (#34) call already exists, and the specs in `api/` cover everything
merged. Paths are under `/api/v1`.

- **Instances and transitions:** `GET /instances`, then `POST /instances/{instance}/completion`, `approval`,
  `approval/reversal` (unapprove), `rejection`, `excusal` and `reassignment`. Conflicts carry codes such as
  `instance_not_open`, `instance_planned` and `instance_closed`.
- **Evidence:** `completion` takes the checklist, note and photos. `GET /instances/{instance}/evidence` returns
  every submission, oldest first, with signed photo links.
- **Media upload:** `POST /media/photos` (multipart `file`), then reference the returned `mediaId` in the
  completion. A kid's photo also sends `capturedAt` and `captureId`. Files are served from `/media/files/{key}`
  by signed link.
- **Review queue:** the approve and reject transitions above work today. The list and bulk-approve endpoints are
  PR #90 (#28).
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
