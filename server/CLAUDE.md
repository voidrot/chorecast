# CLAUDE.md — server

Extends the root CLAUDE.md (repo-wide commands and PR rules live there; not repeated here). This covers the Go module in `server/` and `api/`, whose OpenAPI files are generated from it.

## Commands

Run from the repo root with mise, or from `server/` with plain Go.

| Task | mise | Raw form |
|---|---|---|
| Run the API on :8080 | `mise run dev:server` | `go run ./cmd/chorecast serve` |
| All server tests | `mise run test:server` | `go test ./...` |
| One package | | `go test ./internal/jobs` |
| One test with race detector | | `go test -race -count=1 -run '^TestChoreDefinitions$' ./internal/api` |
| Lint | `mise run lint:server` | `golangci-lint run ./...` |
| sqlc | `mise run gen:sql` | `sqlc generate` |
| OpenAPI 3.1 + 3.0 | `mise run gen:openapi` | `go run ./cmd/chorecast openapi` (and `openapi -3.0`) |
| Every generator | `mise run gen` | |
| New migration | `mise run db:new <name>` | `goose -s create <name> sql` in `internal/db/migrations` |

- `mise run dev:server` sets `CHORECAST_DATA_DIR=.data` and `CHORECAST_DEV=true` (`[env]` in `mise.toml`). A raw run defaults to `./data`.
- The first start with no household logs a `Setup required: open …` URL with a setup token.
- CI runs `mise run gen`, then fails if `server/internal/db/sqlc`, `api/openapi.json`, `api/openapi-3.0.json` or `web/src/api/schema.d.ts` changed. Commit regenerated output.
- CI also runs `go test -race ./...` in `server/`. Use the race form for changes touching jobs, events or rate limits.
- `go build` works without the web admin. `/` answers 404 until `mise run build:web` fills `internal/webui/dist` (git tracks only `.gitkeep` there).

## Lint

`server/.golangci.yml` (v2) enables bodyclose, errorlint, forbidigo, gosec, misspell, noctx and revive (its `exported` rule is off), plus gofmt and goimports. `internal/db/sqlc` is excluded.

- forbidigo rejects `time.Now`, `time.Since` and `time.Until` outside `internal/clock`. Take a `clock.Clock` instead.

## Wiring

`cmd/chorecast/main.go` has four commands: `serve` (default), `admin`, `openapi` and `version`. `serve()` opens the database (which runs migrations), builds the background pieces in `newBackground` (`background.go`), runs `runner.Recover`, and starts the runner, scheduler and event bus on a context cancelled at shutdown. Then it builds a chi router with `RequestID` and `Recoverer`, calls `api.New(r, api.Deps{…})`, and mounts `webui.Handler()` on `/*`.

`internal/api/api.go` mounts Huma under `/api/v1` through `humachi`. Unknown paths and methods under the prefix return problem JSON.

- Middleware order matters: `setup.Gate`, then `auth.Middleware`, then `idempotency.Middleware`. Idempotency needs the principal that auth resolves.
- Each resource has a `registerX(hapi, h)` in `internal/api/<resource>.go`. The `handlers` struct holds the services.
- `printOpenAPI` calls `api.New` with no database and no setup service. Registration must not touch `Deps` at build time.
- `api.New` panics if any operation lacks an access policy (`auth.CheckOperations`). `TestEveryOperationDeclaresAccessPolicy` checks the same thing.

## Packages

- `internal/db`: `Open` sets WAL and foreign keys, pins the pool to one connection, and runs embedded goose migrations. `migrations/` holds the schema, `queries/` holds sqlc input, `sqlc/` holds generated code.
- Domain: `chores` (definitions), `schedule` (rules), `assign` (who gets each occurrence; pure and deterministic), `horizon` (materializes chore instances for [today, today+7]), `dayclose` (day and week close jobs: open instances become missed, with their penalty; hooks run after a close), `streaks` (daily and per-chore streaks and milestone bonuses, evaluated from the day-close hooks), `kids` (profiles and slugs), `ledger` (points entries and balances), `household`, `parents`, `account`, `sso`, `devices`, `setup`, `media` (photo upload and processing, storage, signed URLs).
- Cross-cutting: `apierr`, `paging`, `idempotency`, `auth`, `clock`, `caltime`, `events`, `jobs`, `webui`.
- Services take `(*db.DB, clock.Clock)`. `chores.New` and `kids.NewService` also take the bus; `household.New` takes the bus instead of the clock, and `ledger.New` takes `(*db.DB, *events.Bus, clock.Clock)`.

## Adding an endpoint

1. **Schema.** Before v1, `00001_init.sql` is edited in place; use a new data dir afterwards (`docs/handoff.md`). After v1, use `mise run db:new` and keep the migration forward-only (`docs/roadmap.md`).
2. **Query.** Add a `-- name: X :one|:many|:exec` block to `internal/db/queries/<resource>.sql`.
3. **sqlc.** Run `mise run gen:sql`. Don't edit `internal/db/sqlc` by hand.
4. **Service.** Put the logic in the domain package. Multi-statement writes use a transaction: `db.SQL.BeginTx`, then `db.WithTx(tx)`, with a deferred rollback. If the write also emits an event, use `bus.InTx` and touch only `tx.Q` inside it, since the connection pool has one connection.
5. **Handler.** Add `huma.Register` in `internal/api/<resource>.go`. Set `OperationID`, `Tags`, `Security` or `Metadata`, `Errors`, and `DefaultStatus: http.StatusCreated` for creates.
6. **Errors.** Return `apierr.New(status, apierr.CodeX, detail)`. Add new codes as constants in `apierr`. Never rename one.
7. **Regenerate.** Run `mise run gen`, then commit the spec, sqlc output and `web/src/api/schema.d.ts`.
8. **Clients.** Web imports the regenerated `schema.d.ts`. Android generates its client at build time from `api/openapi-3.0.json`, so only Kotlin code changes there.

No extra step is needed for idempotency. `idempotency.AddHeaderParam` documents `Idempotency-Key` on every POST, and the middleware applies it to authenticated JSON POSTs.

## Auth

- Every operation declares `auth.Require(scopes…)`, which accepts a bearer token or the session cookie, or `auth.Public()`. An operation without either gets 403 `no_access_policy`.
- Scopes live in `internal/auth/scope.go`. Pick the narrowest one. Parent-only writes use `household:manage`. Owner-only capabilities use `server:manage` or `parents:manage`.
- Scope sets are per principal kind: parent, owner, kid, shared device, display, and API key. API keys hold no scopes yet and are denied everything.
- Scopes don't limit data. A handler serving one kid's data must check `Principal.CanAccessKid(id)`. The points handlers (`pointsKid` in `internal/api/points.go`) and `GET /instances` show the pattern.
- Bearer tokens accept `app`, `device`, `device_session`, `display` and `api`. `web_session` is accepted only as the cookie (`authenticate` in `internal/auth/middleware.go`).
- Read the caller with `auth.PrincipalFrom(ctx)`. Operations with a policy always have a principal.
- Credential endpoints use `auth.NewLimiter`. Sensitive parent changes re-check the password and answer 403 `invalid_password`, not 401.
- Sign-in, TOTP, SSO, pairing and PIN elevation are described in `docs/architecture.md` ("Flows"). Don't copy them here.

## Time

- Inject `clock.Clock`. `clock.Real{}` is constructed only in `cmd/chorecast`. Tests use `clock.NewFake(start)` with `Set` and `Advance`.
- Household-local times go through `caltime.Household` (`At`, `DayClose`, `WeekStartOf`, `NextDayClose`), not `time.Date`. DST rules are in `docs/architecture.md` ("Time & calendars").

## Jobs and events

- Register job kinds in `newBackground` (`cmd/chorecast/background.go`) with `runner.Register(kind, handler, jobs.KindOptions{…})`. Enqueue with `Enqueue(ctx, kind, payload, jobs.EnqueueOptions{Key, RunAt})`. A non-empty `Key` makes the enqueue idempotent.
- Recurring work is a `jobs.Provider` added with `sched.Add`: `Every`, `KidDayCloses`, `KidWeekCloses` or `HouseholdDayCloses`.
- Handlers must be idempotent. Delivery is at-least-once.
- Events are typed structs with `EventType()`. They are emitted inside `bus.InTx` with `tx.Emit`. Subscribers are registered with `bus.Subscribe(eventType, name, handler, opts)` in `background.go`, and decode with `events.Decode[T]`.
- Events today: `household.calendar_changed`, `points.entry_posted`, `chore.changed`, `kid.changed` and `instance.missed`. The horizon generator subscribes to the calendar, chore and kid events (one "regenerate everything" handler); `chores.New` and `kids.NewService` take the bus (nil = no events), like `household.New`.

## Web admin embed

`internal/webui/webui.go` embeds `dist/` with `//go:embed all:dist`. `web/vite.config.ts` writes its build to `../server/internal/webui/dist`. The handler serves files, sends `assets/` with immutable caching, and falls back to `index.html` for client routes. `mise run build:server` depends on `build:web`.

## Tests

- API tests live in package `api_test`. `newSetupServer` (`internal/api/setup_test.go`) and `newAuthServer` (`auth_test.go`, which also completes setup) open a real SQLite database in `t.TempDir()`, use a fake clock at 2026-10-31 12:00 UTC, and provide `s.do(method, path, body, cookies…)` and `s.bearer`.
- Assertions check status and the `apierr` code with `wantCode(t, what, rec, status, code)`. Use `decode[T]` for bodies.
- Domain tests build their own fixture, such as `newEnv(t, start)` in `jobs_test.go`, `newFixture` in `auth_test.go`, and `newHarness` with `humatest` in `idempotency_test.go`.
- Use `t.Context()`, `t.TempDir()`, and `t.Cleanup(store.Close)`.

## Docs

`docs/architecture.md` is the design reference for auth flows, API conventions, time, jobs and events. Feature specs are in `docs/features/`. Where the code and a doc disagree, the code wins, and the doc needs fixing.
