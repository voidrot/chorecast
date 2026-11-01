# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

mise pins the tool versions and runs every task (`mise.toml`). Run `mise install`, then `mise run setup`
(lefthook hooks, pnpm packages, Go modules). `mise tasks` lists all tasks.

| Task | What it does |
|---|---|
| `mise run dev` | API on :8080 (`dev:server`) and Vite on :5173 (`dev:web`), which proxies `/api` to :8080 |
| `mise run gen` | All generated code: `gen:sql`, `gen:openapi`, `gen:web-client` |
| `mise run gen:sql` | sqlc: `server/internal/db/queries` and migrations to `server/internal/db/sqlc` |
| `mise run gen:openapi` | Exports `api/openapi.json` (3.1) and `api/openapi-3.0.json` from the Go server |
| `mise run gen:web-client` | `openapi-typescript` writes `web/src/api/schema.d.ts` (runs `gen:openapi` first) |
| `mise run gen:avatars` | Regenerates the bundled kid avatars; not part of `gen` |
| `mise run lint` | `lint:server` (golangci-lint) and `lint:web` (Biome, then typecheck) |
| `mise run test` | `test:server` (`go test ./...`) and `test:web` (Vitest) |
| `mise run fmt` | `gofmt -w` on `server/`, Biome write on `web/` |
| `mise run build` | Builds the web admin, then the server binary at `dist/chorecast` |
| `mise run ci` | What CI runs for server and web: sqlc, lint, test, server build |
| `mise run db:new <name>` | Creates a goose migration in `server/internal/db/migrations/` |

Android has its own tasks, all run through Gradle in `android/`: `build:android`, `lint:android`,
`test:android`, and `android:sdk` (one-time SDK install, accepts licenses). `build:image` builds both container
targets with buildx.

Non-obvious:

- **Generated files are committed, and CI fails on drift.** CI runs `mise run gen`, then fails if `git status`
  lists changes (including new untracked files) under `server/internal/db/sqlc`, `api/openapi.json`,
  `api/openapi-3.0.json` or `web/src/api/schema.d.ts`. After changing SQL, an API operation or its types, run
  `mise run gen` and commit the output.
- **The Kotlin API client is not committed.** `android/api-client` generates it at Gradle build time from
  `api/openapi-3.0.json`, so the spec check above covers it.
- **Web build output is not committed.** `build:web` writes to `server/internal/webui/dist/`, which is git-ignored
  except `.gitkeep`. Until it is built, the server answers the admin URLs with "web admin not built".
- **Git hooks** (`lefthook.yml`, installed by `mise run setup`). `pre-commit` runs gofmt on staged Go files,
  `golangci-lint --new-from-rev=HEAD` (only new findings) and Biome on staged web files. `commit-msg` rejects
  messages whose first line is not a Conventional Commit.
- CI runs server tests a second time with `go test -race ./...`, so a race can fail CI even when `mise run test`
  passes locally.

## Architecture

`docs/architecture.md` has the decisions and their reasons. This is the summary that spans areas.

**The admin ships inside the server.** `web/` builds with Vite into `server/internal/webui/dist`. The `webui`
package embeds it with `//go:embed all:dist` and serves it, falling back to `index.html` for client-side routes.
The API lives under `/api/v1`. The result is one binary that serves both. The Dockerfile builds the web stage
first and copies `dist` into the Go build.

**OpenAPI flows from Go to every client.** The Go handlers are the source of truth. Huma v2 (on chi, through
`humachi`) builds the spec from the operation types. `chorecast openapi` prints it, and `gen:openapi` commits it to
`api/`. The web client types come from the 3.1 spec (`openapi-typescript`, used with `openapi-fetch`). The Android
client comes from the 3.0 copy, which client generators such as openapi-generator support fully.

**Data is one SQLite file.** `db.Open` creates `chorecast.db` in `CHORECAST_DATA_DIR` (default `./data`; mise sets
`.data/` in this repo), in WAL mode with foreign keys on and a single connection. The driver is `modernc.org/sqlite`,
which is pure Go, so no CGO is needed (`build:server` sets `CGO_ENABLED=0`). goose migrations (`NNNNN_name.sql`) are
embedded and applied at startup, so self-hosters run nothing by hand. Queries are SQL in
`server/internal/db/queries/`, and sqlc compiles them to typed Go in `server/internal/db/sqlc/`. Never edit that
output directly. Background jobs run in the same process from the `jobs` table. Delivery is at-least-once, so job
handlers must be idempotent.

**Auth model.** There are no JWTs. Tokens are opaque random values and only their SHA-256 is stored.
- *Parents* have a username and an argon2id password, optionally TOTP and OIDC SSO. They use a `chorecast_session`
  cookie on the web or a bearer token in the app.
- *Kids* have no login. A parent pairs a kid's own phone with a QR code, which yields a `device` token. A shared
  family tablet holds a device token and switches between kid profiles, optionally with a PIN.
- Other token kinds (display, API key) are described in `docs/architecture.md`.
- Every Huma operation declares a policy, `auth.Require(scopes…)` or `auth.Public()`. `api.New` panics if one
  declares neither, and the middleware refuses such operations at runtime.
- Scopes answer "may this principal call this operation". Data scoping is a separate check, `Principal.CanAccessKid`.

**Release and versioning.** release-please (`release-please-config.json`) opens one release PR per component:
`server`, `web` and `android`. `web` is linked to `server` so both share a version, since the admin ships in the
binary, and it has no GitHub release of its own. A merged release PR tags `server-vX.Y.Z` or `android-vX.Y.Z`.
Before 1.0, `feat` bumps minor and `fix` bumps patch. `docs/releasing.md` covers the rest, including signing.

**Image variants.** Both variants run as uid 65532, so one `/data` volume works with either.
- `runtime` (default): distroless static, published as `ghcr.io/voidrot/chorecast:X.Y.Z`, `:X.Y`, `:latest`.
- `media`: Alpine with ffmpeg for video transcoding, published with the same tags plus `-media`.

Android ships two flavors, `play` and `foss`. Its `versionCode` is derived from `versionName`
(`android/app/build.gradle.kts`).

## Area guides

- `server/CLAUDE.md`: Go server packages, Huma operations, auth, database and jobs, and how to test them.
- `web/CLAUDE.md`: Vue admin SPA structure, use of the generated API types, and its lint and test setup.
- `android/CLAUDE.md`: Kotlin app modules, flavors, the generated client and Gradle tasks.

## Pull requests and commits

The changelog is generated by release-please from the commits on `main` (see `docs/releasing.md`). PRs are
squash-merged, and the squash commit is the **PR title** plus the **PR description**, so the commits on a branch
never reach `main` on their own.

- **One changelog entry per PR.** Keep each PR to a single feature, fix or refactor. If a task covers several
  user-visible changes, split it into a stack of small PRs, one per change, instead of one large PR.
- **The PR title is the release note.** It must be a Conventional Commit with the same rules as the lefthook
  `commit-msg` hook: `type(scope)!: subject`, types `feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert`,
  optional lowercase scope (`server`, `web`, `android`, `api`, `deps`, …). Write the subject for someone reading
  the changelog. `feat` bumps minor, `fix` bumps patch, and `!` or a `BREAKING CHANGE:` footer marks a breaking
  change.
- **Exception: a PR that can't be split.** Add each extra change as its own unindented Conventional Commit line at
  the **bottom** of the PR description, after everything else; release-please turns each one into a changelog
  entry. Bullets such as `* fix: …` or `- fix: …` are not picked up.

  ```text
  Short summary of the PR.

  fix(web): keep the chore form's assignment when saving
  feat(server): add defaultChoreMinutes household setting
  ```

- **Don't add trailers** such as `Co-Authored-By` or `Claude-Session` to commits or PR descriptions.
- Branch names follow the type: `feat/…`, `fix/…`, `ci/…`, `docs/…`.
