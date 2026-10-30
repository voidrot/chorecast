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
| Android | **Native Kotlin + Jetpack Compose** (Material 3), AGP 9 built-in Kotlin, version catalog | First-class kiosk/notification/widget support; conventional so it's easy to learn. |
| Android API client | **openapi-generator `kotlin` (jvm-ktor + kotlinx.serialization)** — *to be wired up* | Generated from the same `api/openapi.json`. |
| Push notifications | **UnifiedPush** primary, **FCM fallback** in the `play` flavor only | Works fully self-hosted (ntfy etc.) and on de-Googled phones; `foss` flavor has no Google deps. |
| JS package manager | **pnpm** | Pinned by mise. |
| Git hooks | **lefthook** | gofmt/golangci-lint/eslint on staged files, Conventional Commit check. |
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
  external OIDC identity (`oidc_subject`). First-run creates the initial parent account.
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

### Flows

1. **Device pairing (kids / shared tablet).** Parent opens *Devices → Pair* in the web admin or parent app,
   picks "for <kid>" or "shared family device". Server creates a short-lived (5 min) single-use pairing code and
   shows a QR containing `{serverUrl, code}`. The Android app scans it, `POST /api/v1/devices/pair` → receives
   a long-lived `device` token stored in EncryptedSharedPreferences/Keystore.
2. **Shared tablet profile switch.** Device token + `POST /api/v1/devices/session {userId, pin?}` → a short-lived
   kid session scoped to that device. Auto-locks back to the picker after inactivity.
3. **Parent login in the app, password.** `POST /api/v1/auth/login` → `app` token.
4. **Parent login in the app, OIDC (server-brokered).** App opens a Custom Tab to
   `/api/v1/auth/oidc/start?client=android&challenge=<pkce>`. The server does the full OIDC code flow with the
   IdP, then redirects to `chorecast://auth?code=<one-time>`. The app exchanges `code + verifier` at
   `/api/v1/auth/oidc/exchange` for an `app` token. The app never talks to the IdP and needs no IdP client
   registration of its own.
5. **Web login.** Same password/OIDC handlers, issuing the cookie session instead.

### Authorization

Two roles are enough for v1: `parent` (everything) and `child` (own chores/points, submit completions).
Enforced in a Huma middleware that resolves the principal from cookie or bearer and per-operation
`Security`/role metadata.

## API conventions

- Base path `/api/v1`; spec at `/api/v1/openapi.json`, interactive docs at `/api/v1/docs`.
- `GET /api/v1/version` returns `apiVersion`; the app refuses to talk to a server with an incompatible major.
- Errors are RFC 9457 problem+json (Huma default).

## Push notifications

Server keeps a `push_endpoints` table per device. Delivery:
- **UnifiedPush**: device registers its distributor endpoint (e.g. the household's ntfy); server POSTs to it.
- **FCM fallback** (`play` flavor only): requires a Firebase project; since self-hosters can't each hold our
  FCM credentials, this goes through a UnifiedPush ↔ FCM gateway. Decide gateway hosting before implementing.
- Always complemented by WorkManager periodic sync + local reminder notifications so the app works with no push at all.

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

1. **Domain model**: chores (recurring schedules, rotation), completions + parent approval, behavior points
   (positive/negative), rewards & redemption, allowance. Next planning session.
2. Implement auth (first-run setup, login, sessions, pairing) + Huma auth middleware.
3. Wire openapi-generator into the Android Gradle build (`:api-client` module).
4. Android release signing (keystore via GitHub secrets) and `versionCode` strategy (derive from version or run number).
5. **Play Families policy** review: kids are users, so data-safety form, no ads SDKs, and privacy policy are required.
6. Trusted-proxy handling for client IPs (explicit `CHORECAST_TRUSTED_PROXIES`, not blanket `X-Forwarded-For`).
7. Helm chart in `deploy/helm` (single replica + PVC; SQLite means no horizontal scaling).
8. Backups: `chorecast backup` command using SQLite `VACUUM INTO`.
9. The `applicationId` `io.github.voidrot.chorecast` is permanent once on Play — confirm before first upload.
