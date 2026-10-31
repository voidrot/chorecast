# Chorecast architecture & repo decisions

Status: accepted (initial planning session, 2026-10-30). Revisit any item by adding a dated note below it.

## Product constraints that drove the choices

| Constraint | Consequence |
|---|---|
| **Self-hosted, single household** per instance | No tenant scoping; SQLite is plenty; one binary. |
| Runs on **Docker/compose, Kubernetes, and low-power ARM (Raspberry Pi)** | Small static binary, no CGO, multi-arch image, low idle RAM. |
| **Three kinds of app users**: parents, kids on their own device, shared family tablet | Kids can't be expected to have email/passwords → device pairing + profile/PIN. |
| **Web admin + Android app** | Server embeds the admin SPA; the app is for day-to-day use. |
| Android distributed via **GitHub Releases APK and Google Play** | Two build flavors; Play's Families policy applies (kids are users). |

## Stack

| Area | Decision | Why |
|---|---|---|
| Tool versions & tasks | **mise** (`mise.toml`) | One file pins Go/Node/pnpm/JDK/Android cmdline-tools/sqlc/goose/linters; `mise run <task>` is identical locally and in CI. |
| Server language | **Go** | ~20–40 MB RAM, static ARM64 binary, fast builds, you already know it. |
| HTTP router | **chi v5** | Thin, stdlib-compatible, route groups/middleware. |
| API contract | **Code-first OpenAPI via Huma v2** (`humachi` adapter) | Go handler structs are the source of truth; spec exported to `api/openapi.json` and committed. |
| Database | **SQLite** (`modernc.org/sqlite`, pure Go, WAL) | Zero extra containers, trivial backups (copy one file), fine at household scale. |
| DB access | **sqlc** (type-safe generated Go from SQL) | Write real SQL, get typed functions; no ORM magic. |
| Migrations | **goose**, embedded and run at startup | Self-hosters never run a migration step by hand. `mise run db:new <name>` creates one. |
| Web admin | **Vue 3 + Vite + TypeScript SPA**, **Naive UI** + **Tailwind CSS 4**, Pinia, Vue Router | Built into `server/internal/webui/dist` and embedded with `go:embed` → one artifact. |
| Web API client | **openapi-typescript + openapi-fetch** | Typed `api.GET('/health')` from the generated spec, tiny runtime. |
| Web lint/format | **Biome** (with experimental full Vue support) | One fast tool replaces ESLint + Prettier. |
| TypeScript | **TS 7** (native compiler) as `tsc`; `vue-tsc` checks `.vue` files | Volar/vue-tsc and openapi-typescript still need the TS 6 JS API, so `web/.pnpmfile.cjs` gives just those packages a private TS 6. Remove the shim once they support TS 7. |
| Android | **Native Kotlin + Jetpack Compose** (Material 3), AGP 9 built-in Kotlin, version catalog | First-class kiosk/notification/widget support; conventional so it's easy to learn. |
| Android API client | **openapi-generator `kotlin` (jvm-ktor + kotlinx.serialization)** — *to be wired up* | Generated from the same `api/openapi.json`. |
| Push notifications | **UnifiedPush** primary; **FCM only with the admin's own Firebase project** (`play` flavor); polling fallback | Content-free pushes, no project-run relay; `foss` flavor has no Google deps. |
| JS package manager | **pnpm** | Pinned by mise. |
| Git hooks | **lefthook** | gofmt/golangci-lint/biome on staged files, Conventional Commit check. |
| CI | **GitHub Actions** calling `mise run …` | No duplicated logic between local and CI. |
| Releases | **Conventional Commits + release-please** (manifest mode) | Independent versions: `server` (web is linked to it) and `android`. |
| Container | Multi-stage `deploy/Dockerfile` → `distroless/static:nonroot`, amd64 + arm64, pushed to GHCR | Small, no shell, non-root. |

## Repository layout

```
chorecast/
├── mise.toml                 # tools + every repo task
├── lefthook.yml              # git hooks
├── api/openapi.json          # generated from Go (mise run gen:openapi) — committed, consumed by web & android
├── server/                   # Go module github.com/voidrot/chorecast/server
│   ├── cmd/chorecast/        # `chorecast serve | openapi | version`
│   ├── internal/api/         # Huma operations (one file per resource as it grows)
│   ├── internal/config/      # CHORECAST_* env config
│   ├── internal/db/          # Open + embedded goose migrations
│   │   ├── migrations/       # NNNNN_name.sql
│   │   ├── queries/          # sqlc input
│   │   └── sqlc/             # generated — committed, CI checks for drift
│   ├── internal/webui/       # go:embed of the built Vue SPA (dist/ is build output)
│   └── internal/version/     # ldflags-injected version + APIVersion
├── web/                      # Vue admin SPA (pnpm)
├── android/                  # Gradle project, :app module, flavors play/foss
├── deploy/                   # Dockerfile, compose.yaml (Helm chart later)
└── docs/
```

## Authentication & authorization

Hand-rolled on small, auditable libraries — no auth framework:
`golang.org/x/crypto/argon2` (hashing), `coreos/go-oidc` + `x/oauth2` (SSO), `pquerna/otp` (TOTP),
`alexedwards/scs` (web cookie sessions, SQLite store).

### Identities

- **Parent** (`role = parent`): username + argon2id password, optional TOTP. Optionally linked to an
  external OIDC identity (`oidc_subject`). First-run creates the initial parent account. Optional
  **parent PIN** (4–8 digits, argon2id) used only to elevate a shared-tablet session (see below).
- **Child** (`role = child`): no username/password. Optional 4–6 digit PIN (argon2id), required or not per
  parent setting.

### Credentials / tokens

All non-cookie credentials are **opaque random tokens** (32 bytes, base64url); only `SHA-256(token)` is stored
in `tokens`. Revocation is a row delete. No JWTs (single node, nothing to gain, key management to lose).

| Kind | Holder | How obtained | Grants |
|---|---|---|---|
| `web_session` | Browser (HttpOnly, Secure, SameSite=Lax cookie) | Password(+TOTP) or OIDC login on the web | Full parent access |
| `app` | Parent's phone | In-app login (password or brokered OIDC) | Full parent access |
| `device` | Kid's phone or shared tablet | **QR pairing** by a parent | Bound to one kid, or "shared" (profile picker); kid-level access only |
| `display` | Wall tablet / TV browser (family dashboard) | Code/QR shown on the display, approved by a parent | Read-only kid-visible household data |
| `api` | Scripts and external systems (post-v1) | Created in Settings → Integrations | Explicit scopes, optional kid restriction; see [features/integrations.md](features/integrations.md) |

### Flows

0. **First run** (`internal/setup`). With no household in the database, the server stores a fresh setup token
   (SHA-256 only) on every start and logs `Setup required: open <url>/setup?token=…` (base URL from
   `CHORECAST_PUBLIC_URL`, else `http://localhost:<port>`). A Huma gate answers every operation not marked
   `setup.AllowDuringSetup` (health, version, setup) with 503 `setup_required`. `POST /setup/verify` checks a token
   without using it; `POST /setup` validates, then creates the household, first owner and kids in one transaction,
   deletes the token and sets the owner's session cookie. Afterwards the setup endpoints answer 409
   `setup_complete`. Session cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` when the request came over HTTPS
   (directly or `X-Forwarded-Proto: https`), so plain-HTTP LAN installs keep working.
1. **Device pairing (kids / shared tablet).** Parent opens *Devices → Pair* in the web admin or parent app,
   picks "for <kid>" or "shared family device". Server creates a short-lived (5 min) single-use pairing code and
   shows a QR containing `{serverUrl, code}`. The Android app scans it, `POST /api/v1/devices/pair` → receives
   a long-lived `device` token stored in EncryptedSharedPreferences/Keystore.
2. **Shared tablet profile switch.** Device token + `POST /api/v1/devices/session {userId, pin?}` → a short-lived
   kid session scoped to that device. Auto-locks back to the picker after inactivity.
   **Parent elevation:** device token + `POST /api/v1/devices/elevate {userId, parentPin}` → a ~2 min parent session
   scoped to that device (attributed to that parent). 5 failed PINs lock elevation on that device for 15 min.
3. **Parent login, password** (`internal/account`). `POST /api/v1/auth/login {username, password, client}`:
   `client: app` returns an `app` bearer token (no expiry, labelled with the device name); `client: web` sets the
   30-day session cookie. With TOTP on, a correct password alone answers 401 `totp_required`; the client resends
   with `totpCode` (±1 step of drift, each step accepted once via `users.totp_last_step`) or a single-use
   `recoveryCode` (10, SHA-256-hashed, shown once). Unknown usernames still pay one argon2 verification.
   Failures are rate limited in memory: 5 per username+IP and 20 per IP per 15 min, 50 per username per hour
   (429 + Retry-After). Self-service lives under `/account/*` (scope `account:manage`): sessions list/revoke,
   password change (signs out other sessions), parent PIN, TOTP setup/enable/disable, recovery code
   regeneration; sensitive changes re-check the password (403 `invalid_password`, never 401, so clients don't
   treat it as a lost session). `POST /auth/logout` revokes the calling session or app token.
4. **Parent login in the app, OIDC (server-brokered).** App opens a Custom Tab to
   `/api/v1/auth/oidc/start?client=android&challenge=<pkce>`. The server does the full OIDC code flow with the
   IdP, then redirects to `chorecast://auth?code=<one-time>`. The app exchanges `code + verifier` at
   `/api/v1/auth/oidc/exchange` for an `app` token. The app never talks to the IdP and needs no IdP client
   registration of its own.
5. **Web login.** Same password/OIDC handlers, issuing the cookie session instead. The TOTP secret is stored in
   plain base32 in the database (needed to verify codes); protect the data directory and backups accordingly.

### Authorization

Implemented in `internal/auth`:

- **Every operation declares a policy**: `Security: auth.Require(auth.ScopeChoresRead, ...)` or
  `Metadata: auth.Public()`. `api.New` panics and a test fails if any operation declares neither, and the
  middleware refuses such operations at runtime (fail closed).
- **Principals** are resolved by one Huma middleware: a `Bearer` token (app, device, display, API key) or the
  `chorecast_session` cookie (web sessions only; a session token is not accepted as a bearer and vice versa).
- **Scope sets per principal kind** (`scope.go`): parent; owner = parent + `server:manage`; kid (read own data,
  complete chores); shared device (read profiles, pick a kid); display (read-only); API key (no scopes until
  per-key grants exist post-v1).
- **Data scoping** is separate from scopes: handlers call `Principal.CanAccessKid(id)`. Parents and displays cover
  every kid; a kid only themselves; shared devices and API keys none.
- Handlers read the caller with `auth.PrincipalFrom(ctx)`.
- Passwords and PINs use argon2id (OWASP minimum: 19 MiB, t=2, p=1) in PHC format; `VerifySecret` reports when a
  stored hash should be upgraded.
- Token expiry uses the injected clock; `last_used_at` writes are throttled to once a minute per token.

## API conventions

- Base path `/api/v1`; spec at `/api/v1/openapi.json`, interactive docs at `/api/v1/docs`. JSON field names are
  camelCase.

**Versioning & compatibility**
- `GET /api/v1/version` returns `apiVersion`, the integer major in the URL prefix (a test enforces they match).
- Within `/api/v1`, changes are **additive only**: new endpoints, new optional parameters, new response fields,
  new enum values, new error codes. Clients must ignore unknown fields and treat unknown enum values and codes
  generically.
- A breaking change ships as `/api/v2` with `apiVersion: 2`. The server keeps serving the previous major for at
  least one minor release, so an un-updated app keeps working while the household upgrades it.
- The Android app checks `apiVersion` at startup and shows an "update the app/server" message when it can't
  speak the server's major.

**Errors** (`internal/apierr`)
- Every error is RFC 9457 `application/problem+json`: `type`, `title`, `status`, `detail`, `errors[]`, plus a
  stable **`code`** (`kid_not_found`, `validation_failed`, `insufficient_scope`, ...). Clients branch on `code`,
  never on `detail` text. Codes are never renamed.
- Handlers return `apierr.New(status, code, detail)`; plain Huma errors get a default code per status. Unknown
  paths and methods under `/api/v1` answer in the same format.

**Pagination** (`internal/paging`)
- List operations embed `paging.Params` (`?cursor=&limit=`, limit 1–100, default 50) and return
  `{ "items": [...], "nextCursor": "..." }`; `nextCursor` is absent on the last page.
- Cursors are opaque (versioned base64url keyset). Handlers fetch `limit+1` rows after the decoded key and call
  `paging.Build`. A malformed cursor is a 400 `invalid_cursor`.

**Idempotent retries** (`internal/idempotency`)
- Authenticated JSON `POST`s may send `Idempotency-Key` (1–255 visible ASCII chars, documented in the spec on
  every POST). A retry with the same key and body replays the first response with `Idempotent-Replayed: true`
  instead of running again; the same key with a different body is a 422 `idempotency_key_reuse`; a retry while
  the first request is still running is a 409 `idempotency_key_in_progress`.
- Keys are scoped to the caller's token and kept 24 h (purged by daily maintenance). 5xx responses are not
  stored, so the client can retry for real. A key stuck in progress for 5 minutes (crash) is reclaimed.

**Kid references** (`internal/kids`)
- Wherever a path takes a kid, it accepts the numeric id or the kid's **slug** (`/kids/sam`). Slugs are derived
  from the display name at creation (`Zoë Smith` → `zoe-smith`, `-2` on collision), are never all digits, and
  don't change when the name does. Unknown kids are a 404 `kid_not_found`.

## Android app structure

Three Gradle modules (`android/`):

| Module | What | Tests |
|---|---|---|
| `:api-client` | Kotlin client **generated at build time** by openapi-generator (`kotlin`, `jvm-ktor`, kotlinx.serialization) from `api/openapi-3.0.json`. Nothing generated is committed, so it can't drift; the committed spec is drift-checked in CI. | — |
| `:core` | Plain Kotlin/JVM logic: `ServerUrl` parsing, `Compatibility` (API major check), `ServerChecker` (`GET /version` via the generated client), `TokenStore` interface, shared client config (`ApiClients`: JSON with `ignoreUnknownKeys`, per the compatibility policy). | JUnit + ktor `MockEngine`, run on any JVM |
| `:app` | Compose UI: type-safe navigation (`ui/Routes.kt`), screens + ViewModels (`StateFlow` state), manual DI via `AppContainer` (created in `ChorecastApp`), `KeystoreTokenStore` (AES-256-GCM key in the Android Keystore), OkHttp engine. All UI text in `strings.xml`. | Android unit tests |

- The generated client registers no JSON converter: always build API clients with `ApiClients.config`.
- Huma's `$schema` response links are disabled server-side; generated models can't decode them.
- Cleartext HTTP is allowed (self-hosted LANs); the connect screen warns when a server URL is `http://`.
- Locally, Maven Central may rate-limit; CI resolves normally.

## Web admin structure

Vue 3 SPA in `web/`, embedded in the server binary. Components come from Naive UI (MIT; light/dark theme follows
the OS via `NConfigProvider` in `App.vue`, brand colors in `src/theme.ts`); layout and spacing use Tailwind 4
utility classes (`src/style.css`). Naive's runtime styles are unlayered, so they win over Tailwind's layered preflight.
(PrimeVue was dropped: v5 moved to a commercial license that needs a key.)


- `src/api/client.ts`: typed `openapi-fetch` client (types generated from `api/openapi.json`). A response
  middleware reports a 401 on an established session (`apiEvents.onUnauthorized`) and 503 `setup_required`
  (`apiEvents.onSetupRequired`); `main.ts` wires these to the session store and router.
- `src/api/errors.ts`: `ApiError` from problem responses (`status`, `code`, `detail`, `fieldErrors`). Use the
  `error` that openapi-fetch returns (it has already read the body). `useAsync()` wraps actions with
  loading/error state.
- `src/stores/session.ts`: loads `GET /me`; status `unknown | anonymous | forbidden | authenticated |
  setup_required`. The admin is for parents, so other principals are `forbidden`.
- `src/views/SetupView.vue` + `src/setup/wizard.ts` (pure, unit-tested): the first-run wizard. The token from the
  setup link moves from the URL to `sessionStorage` and is verified before any form is shown.
- `src/router/guard.ts`: pure `decide(status, route)` (unit-tested): anonymous → `/login?redirect=…`, setup
  pending → `/setup`, signed-in users skip login/setup. Routes reachable without a session set
  `meta.public`.
- `src/i18n.ts` + `src/locales/en.json`: all UI text via `vue-i18n`. `src/format.ts`: instants in the household
  timezone (`householdTimeZone`), civil dates never shifted.

## Time & calendars

- **Never read the wall clock directly.** Code takes a `clock.Clock` (`internal/clock`); tests use `clock.Fake`.
  A forbidigo lint rule rejects `time.Now`, `time.Since` and `time.Until` outside `internal/clock`.
- **Household calendar math lives in `internal/caltime`**: civil `Date`, `TimeOfDay`, and `Household`
  (timezone, day end, week start) with `DayClose`, `DayContaining`, `NextDayClose`, `WeekStartOf`, `WeekClose`,
  and per-kid `EffectiveDayEnd`.
- A **day end** is within its own calendar day, in (00:00, 24:00]; `00:00` means the following midnight. Day *d*
  covers `(DayClose(d−1), DayClose(d)]`, so with an early day end the evening after the close belongs to the next day.
- **DST:** a wall time that occurs twice resolves to the earlier instant; one inside a spring-forward gap resolves to
  the end of the gap (Go's `time.Date` resolves gap times backwards, which would close a day early).
- The timezone database is embedded in the binary (`time/tzdata`), so any container image works.

## Background jobs

`internal/jobs`, running inside the server process (no Redis or worker containers):

- **Queue**: the `jobs` table. `Runner.Enqueue(kind, payload, {Key, RunAt})`; a non-empty `Key` makes the
  enqueue idempotent. Kinds are registered with a queue and max attempts; each queue has its own worker count
  (`SetConcurrency`, e.g. `media` = 1 so transcoding never swamps a Pi).
- **Delivery is at-least-once**: failed jobs retry with backoff (30s, 1m, 2m, ... capped at 1h), panics count as
  failures, and jobs left `running` by a crash are requeued at startup (`Recover`). **Handlers must be idempotent.**
- **Recurring work**: `Scheduler` providers return `Recurring` schedules (`KidDayCloses`, `KidWeekCloses`,
  `HouseholdDayCloses`, `Every`). A cursor per schedule records the last occurrence enqueued; each tick enqueues
  every due occurrence after it with key `<schedule>:<occurrence>`, so runs missed while the server was down are
  caught up and duplicates are impossible. New schedules start from now (no backfill).
- Kinds and schedules are wired in `cmd/chorecast/jobs.go`. Today: `maintenance.daily` (purge finished jobs and
  expired tokens).
- Tests drive time with `clock.Fake` and call `Scheduler.Tick` / `Runner.RunDue` directly.

## Domain events

`internal/events`, a transactional outbox on top of the job runner:

- Domain code changes state and emits events in one transaction:
  `bus.InTx(ctx, func(tx *events.Tx) error { ...tx.Q.UpdateX...; return tx.Emit(ctx, ChoreSubmitted{...}) })`.
  If the function errors or panics, both the state change and the events roll back; if it commits, the events
  are durable even if the process dies a moment later.
- **Inside `InTx`, use only `tx.Q`.** SQLite runs on a single connection, so any other query blocks until the
  transaction ends.
- Events are typed structs with a stable `EventType()` (e.g. `assignment.submitted`); subscribers decode with
  `events.Decode[T]`.
- The dispatcher (woken on commit, polling every second) turns each event into one job per subscriber (key
  `event:<id>:<subscriber>`) and marks it dispatched. Each subscriber therefore retries independently with job
  backoff, and a crash mid-dispatch can't duplicate deliveries.
- Delivery is at-least-once; subscribers must be idempotent (use `Envelope.ID`). Subscribers are registered in
  `cmd/chorecast/background.go`. Dispatched events are purged after 30 days by daily maintenance.

## Push notifications

See [features/notifications.md](features/notifications.md). In short: push payloads carry no content (wake-up
only; the app fetches from the server). Transports are **UnifiedPush** (both flavors), **FCM only via the
admin's own Firebase project** configured at runtime (`play` flavor), and an always-on **polling + local
reminders** fallback. The project operates **no relay or gateway**, so no child data passes through project
infrastructure.

## mise tasks

`mise tasks` lists them all. The important ones:

| Task | Does |
|---|---|
| `setup` | lefthook install, pnpm install, go mod download |
| `android:sdk` | installs SDK platform/build-tools (you accept the SDK licenses interactively) |
| `dev` | Go server on :8080 + Vite on :5173 (proxies `/api`) |
| `gen` | sqlc → OpenAPI export → TS client types |
| `db:new <name>` | new goose migration |
| `lint` / `test` / `build` | server + web |
| `build:android`, `lint:android`, `test:android` | Gradle wrappers |
| `build:image` | multi-arch container via buildx |
| `ci` | what CI runs for server + web |

## Open items / next steps

1. **Feature specs** are in [features/](features/); sequencing is in [roadmap.md](roadmap.md).
2. Implement auth (first-run setup, login, sessions, pairing) + Huma auth middleware.
3. Wire openapi-generator into the Android Gradle build (`:api-client` module).
4. Android release signing (keystore via GitHub secrets). `versionCode` is derived from semver in Gradle
   (`major*1_000_000 + minor*1_000 + patch`).
5. **Google Play after v1** (v1 ships GitHub Release APKs). Before Play: Families policy review, data-safety form, privacy policy, no ads SDKs.
6. Trusted-proxy handling for client IPs (explicit `CHORECAST_TRUSTED_PROXIES`, not blanket `X-Forwarded-For`).
7. Helm chart in `deploy/helm` **after v1** (single replica + PVC; SQLite means no horizontal scaling).
8. Backups: `chorecast backup` command using SQLite `VACUUM INTO`.
9. `applicationId` is **`dev.voidrot.chorecast`** (owner controls voidrot.dev). Permanent once on Play.
