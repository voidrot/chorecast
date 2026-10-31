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
| Web admin | **Vue 3 + Vite + TypeScript SPA**, **PrimeVue** (Aura theme), Pinia, Vue Router | Built into `server/internal/webui/dist` and embedded with `go:embed` → one artifact. |
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

1. **Device pairing (kids / shared tablet).** Parent opens *Devices → Pair* in the web admin or parent app,
   picks "for <kid>" or "shared family device". Server creates a short-lived (5 min) single-use pairing code and
   shows a QR containing `{serverUrl, code}`. The Android app scans it, `POST /api/v1/devices/pair` → receives
   a long-lived `device` token stored in EncryptedSharedPreferences/Keystore.
2. **Shared tablet profile switch.** Device token + `POST /api/v1/devices/session {userId, pin?}` → a short-lived
   kid session scoped to that device. Auto-locks back to the picker after inactivity.
   **Parent elevation:** device token + `POST /api/v1/devices/elevate {userId, parentPin}` → a ~2 min parent session
   scoped to that device (attributed to that parent). 5 failed PINs lock elevation on that device for 15 min.
3. **Parent login in the app, password.** `POST /api/v1/auth/login` → `app` token.
4. **Parent login in the app, OIDC (server-brokered).** App opens a Custom Tab to
   `/api/v1/auth/oidc/start?client=android&challenge=<pkce>`. The server does the full OIDC code flow with the
   IdP, then redirects to `chorecast://auth?code=<one-time>`. The app exchanges `code + verifier` at
   `/api/v1/auth/oidc/exchange` for an `app` token. The app never talks to the IdP and needs no IdP client
   registration of its own.
5. **Web login.** Same password/OIDC handlers, issuing the cookie session instead.

### Authorization

Every operation declares required **scopes**; sessions and tokens map to scope sets (parent, kid, display, API key).
This is built from day one so post-v1 API keys need no redesign.

Two roles are enough for v1: `parent` (everything) and `child` (own chores/points, submit completions).
Enforced in a Huma middleware that resolves the principal from cookie or bearer and per-operation
`Security`/role metadata.

## API conventions

- Base path `/api/v1`; spec at `/api/v1/openapi.json`, interactive docs at `/api/v1/docs`.
- `GET /api/v1/version` returns `apiVersion`; the app refuses to talk to a server with an incompatible major.
- Errors are RFC 9457 problem+json (Huma default).

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
