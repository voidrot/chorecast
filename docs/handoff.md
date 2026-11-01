# Handoff: state of Phase 1 and how to keep going

Written 2026-11-01 for the next agent (or person) picking up Phase 1 (#2). Read this, then
`docs/architecture.md`, `docs/roadmap.md` and the feature spec named in the issue you're working on.

## Where things stand

The issues are worked through in order with one PR each. The owner merges the PRs.

| Issue | What | State |
|---|---|---|
| #12 | First-run token + web setup wizard | merged (#46) |
| #13 | Parent login, TOTP, recovery codes, sessions | merged |
| #14 | Optional OIDC (web + app) | merged |
| #15 | Invites, roles, admin CLI (`admin reset-password\|disable-2fa`) | merged |
| #16 | Kid profiles | merged; issue stays open for **uploaded photo avatars** (deferred to #27, needs the media pipeline) |
| #17 | Household settings | merged |
| #18 | Device pairing, picker, parent PIN elevation | merged; issue open only because the end-to-end check on a real device hasn't been done (no device or emulator here) |
| #19 | Devices page: revoke, remote lock | merged (#54) |
| #20 | Chore definitions + location/equipment catalogs | merged (#55); the schedule editor it was waiting on landed in #56, so **#20 can be closed** |
| #21 | Schedule rules, validation, occurrence preview | merged (#56) |
| #22 | Assignment modes | **PR #57 open, CI green, waiting for review** (this doc rides on that PR's branch) |

**Next up: #23, the rolling 7-day horizon generator.** After that come #24 (day/week close), #26
(ledger), #25 (instance state machine), #27 (evidence), #28 (review queue), #29 (auto-approve) and
#30 (streaks). Then the Android screens (#31–#33), the web admin polish (#34), reminders (#35),
backups (#36), the audit log (#37) and docs (#38). Check each issue's "Depends on" line; #25 needs
the ledger (#26) first.

`docs/roadmap.md` checkboxes have **not** been ticked as things merged. Go by the issue and PR state,
not the boxes.

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
be older, so use `mise run …`. **Commit with `mise exec -- git commit …`**: the lefthook pre-commit hook
runs `golangci-lint`, and the copy on the plain `PATH` is too old for Go 1.27 and fails the hook.

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
`feat: … (#23)`.

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
  `household.calendar_changed` is already emitted when the timezone, day end or week start changes;
  **#23 should subscribe to it** to regenerate.
- `internal/jobs` has a SQLite-backed runner and a scheduler with `KidDayCloses`, `KidWeekCloses` and
  `HouseholdDayCloses` providers (per-kid effective day end). Wire new recurring work in
  `cmd/chorecast/background.go`.

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

## What #23 can build on

- `schedule.Expand(rule, cal, slots, from, to, limit)` / `schedule.Next` give occurrences
  (`Date`, `Slot`, `DueAt`, `Flexible`). Flexible weeks yield `TimesPerWeek` occurrences dated at the
  week's first active day, all due at week close.
- `assign.Planner{Kids, DefaultMinutes, Away, Ledger}.Plan(chore, occurrences, state)` returns
  `[]Planned` (a `KidID`, or a `Group` for shared chores, plus `Auto`) and the next rotation `State`.
  It's pure and deterministic. To keep regenerations reproducible:
  - Feed the ledger the **kept** instances (other chores, earlier days) before planning. Plan chores in
    a stable order (by id) and occurrences in date order.
  - Persist the rotation state as of the **last kept instance** and resume from it, not from the end
    of the forecast. Otherwise each regeneration would advance the rotation again.
    `chores.rotation_state` holds JSON `assign.State` and is reset to `{}` when the mode or kid list
    changes.
  - `assign.SplitPoints` is ready for shared-chore completion (#25/#26).
- Chore columns: `schedule` (JSON, NULL = unscheduled), `slots`, `assign_mode`, `assign_kids`,
  `shared_points`, `shared_penalty`, `disabled`, `min_age` plus `chore_eligibility` overrides,
  `estimated_minutes`. Household: `default_chore_minutes`, `week_start`, `day_end`, `timezone`.
- Kids: `archived_at` means archived (skip them). `birth_date` may be empty (then not age-gated).
  `day_end_override` gives the kid's effective day end (`caltime.Household.EffectiveDayEnd`).
- The spec for "untouched" instances, today's slot rule and "planned" is in
  `docs/features/schedules.md` (Rolling horizon generation).

## Open loose ends

- An **assignment editor in the web UI** doesn't exist yet. The chore form only passes the assignment
  through. It belongs in #34 (or alongside #23 if you want the horizon to be usable from the UI).
- **#16:** uploaded photo avatars (needs #27's media pipeline).
- **#18:** the on-device end-to-end check.
- **#20:** can be closed now that the schedule editor shipped.
- The shared-chore "group" is the chore's own kid list. There's no separate group entity (a design
  call made in #22, documented in `schedules.md`).

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
