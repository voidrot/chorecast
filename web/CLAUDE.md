# CLAUDE.md — web

This extends the root `CLAUDE.md`, which has the repo-wide commands and PR/commit rules. This file covers `web/`, the Vue 3 admin SPA.

## Commands

Run from `web/`, or from the repo root with the `mise run` task in parentheses. Dependencies come from `mise run setup`.

- `pnpm dev` (`dev:web`): Vite on its default port, 5173. `vite.config.ts` proxies `/api` to `http://localhost:8080`, so start the Go server too (`mise run dev:server`, or `mise run dev` for both).
- `pnpm build` (`build:web`): runs `pnpm typecheck`, then `vite build` into `../server/internal/webui/dist`, emptying it first. A type error fails the build. Rebuild the server after a web build, since the binary embeds that directory.
- `pnpm typecheck`: `tsc -b tsconfig.node.json` (config files) and `vue-tsc -b tsconfig.app.json` (`src/`). The app tsconfig excludes `__tests__`, so specs are not typechecked.
- `pnpm lint` / `pnpm format`: `biome check .` / `biome check --write .`. `lint:web` runs lint and typecheck. The lefthook pre-commit hook runs Biome on staged web files.
- `pnpm test` (`test:web`): `vitest run`. One file: `pnpm test src/__tests__/guard.spec.ts`. One test: `pnpm exec vitest run -t "checks the household step"`.
- `mise run gen:web-client`: regenerates `src/api/schema.d.ts` from `api/openapi.json`. It depends on `gen:openapi`, which exports the spec from the Go server. Never hand-edit `schema.d.ts`. CI fails when it's stale (`server-web` job).
- `mise run gen:avatars`: regenerates the avatar SVGs, `src/avatars.ts` and `server/internal/kids/avatars_gen.go`. It is not part of `mise run gen`. Changing the seeds in `scripts/gen-avatars.mjs` changes every kid's avatar.

`.pnpmfile.cjs` gives `vue-tsc`, `@vue/language-core`, `@volar/*` and `openapi-typescript` a private TypeScript 6, because they don't support TS 7 yet. Don't remove it without checking that they work on TS 7.

## Layout

- `src/main.ts` creates the app (Pinia, router, i18n) and wires `apiEvents` to the session store and router. `src/App.vue` provides Naive's config, message and dialog providers.
- `src/views/`: one `*View.vue` per route, grouped by area (`chores/`, `kids/`, `devices/`, `parents/`, `settings/`, `account/`). Cards and editors sit beside their view. Public views are `LoginView`, `SetupView`, `InviteView` and `NotFoundView`. `src/layouts/AppLayout.vue` is the shell for signed-in routes.
- Logic lives in plain `.ts` modules beside its area, not in components: `chores/form.ts`, `chores/schedule.ts`, `kids/form.ts`, `devices/pairing.ts`, `setup/wizard.ts`, `auth/login.ts`, `router/guard.ts`, `format.ts`. Specs test those modules directly.

## API layer

- `src/api/client.ts` exports one `api` (openapi-fetch typed by `paths` from `schema.d.ts`) with base `/api/v1` on the page origin and `credentials: 'same-origin'`. Auth is the HttpOnly session cookie, so the client handles no tokens.
- Views and stores call `api` directly, e.g. `const { data, error, response } = await api.POST('/auth/login', { body })`. There is no service layer.
- Errors are RFC 9457 problem documents. `ApiError` (`src/api/errors.ts`) exposes `status`, `code`, `detail` and `fieldErrors`. Branch on `code`, never on `detail`. A failed request with no response has status 0 and code `network_error`. openapi-fetch has already parsed the body into `error`; `problemFrom(response)` is for code that reads a raw `Response`.
- A response middleware in `client.ts` calls `apiEvents.onUnauthorized` on 401 and `apiEvents.onSetupRequired` on 503 with code `setup_required`. The client doesn't import the router; `main.ts` sets those callbacks.
- `useAsync(fn)` (`src/composables/useAsync.ts`) returns `data`, `error` (always an `ApiError`) and `loading`. `useServerInfo()` fetches `GET /version` once per page load, e.g. to show whether SSO is configured.

## Session, router and setup

- `useSessionStore` (`src/stores/session.ts`) loads `GET /me`. Status is `unknown | anonymous | forbidden | authenticated | setup_required`. A `kind` other than `parent` is `forbidden`, because the admin is for parents only. `logout()` always clears local state, even if the request fails.
- The router's `beforeEach` (`src/router/index.ts`) loads the session when it is `unknown`, then returns `decide(status, to)` from `src/router/guard.ts`. `decide` is pure and table-tested. Routes that need no session set `meta: { public: true }`, and the `RouteMeta` augmentation is in `guard.ts`.
- First run: the server answers 503 `setup_required` until setup is complete, and the guard sends every route to `/setup`. `SetupView` takes `?token=` from the setup link, moves it to `sessionStorage` and out of the URL, then verifies it before showing the wizard. `wizard.ts` validates each step and builds the `POST /setup` body.
- A 401 after sign-in clears the session and redirects to `/login?redirect=…`. `loginFailure()` in `auth/login.ts` maps error codes to a message and a step (password, TOTP or recovery code).
- There is no elevation or re-authentication step in the web app. A grep for `elevat`, `reauth`, `step-up` and `sudo` found nothing.

## i18n and copy

- All UI text lives in `src/locales/en.json`, English only. Read it with `useI18n().t('area.key')`, or `i18n.global.t` outside components.
- Top-level keys are areas that match views (`login`, `setup`, `kids`, `chores`). Nested keys follow the screen and field (`setup.household.nameRequired`).
- Add a string to `en.json`. Other locales would be new files registered in `src/i18n.ts`; there are none yet.
- Pure validators return i18n keys (`householdProblems`, `ownerProblems`) and the view translates them. Keep that split.

## Styling and theme

- Components come from Naive UI. `src/theme.ts` holds the emerald `themeOverrides` and `naiveLocale`, which blanks Naive's placeholders because labels say what to enter. The dark theme follows the OS (`useOsTheme` in `App.vue`).
- Tailwind 4 runs through `@tailwindcss/vite`. There is no `tailwind.config`. `src/style.css` imports Tailwind and sets the base body styles. Use utility classes for layout and spacing.
- Naive injects unlayered styles, so they override Tailwind's base layer. Don't fight that with `!important`.

## Avatars and the embedded build

- Built-in avatars are `public/avatars/pixel-01.svg` … `pixel-24.svg`, generated by `scripts/gen-avatars.mjs` (DiceBear Pixel Art, CC0). Their IDs appear in `src/avatars.ts` and `server/internal/kids/avatars_gen.go`. The server rejects a kid avatar ID not in its list (`kids.go`, "unknown avatar"). `KidAvatar.vue` shows initials when `avatar` is empty.
- `vite.config.ts` writes to `server/internal/webui/dist`. `webui.go` embeds it with `go:embed all:dist` and serves it at `/*`. Unknown paths fall back to `index.html`, so client routes work. Files under `assets/` get an immutable cache header. Without a build, the handler returns 404 with the hint to run `mise run build:web`.
- Git ignores `server/internal/webui/dist/*` except `.gitkeep`, so a fresh clone still compiles. The `build` script touches that `.gitkeep`.

## Tests

- Vitest 5 with `environment: 'jsdom'` (set in `vite.config.ts`). Specs live in `src/__tests__/*.spec.ts`. `pnpm test` runs in `mise run ci`.
- Specs test pure modules directly: `guard.spec.ts` table-tests `decide()`, `setup.spec.ts` covers the wizard's validators and `buildRequest`.
- `api.spec.ts` is the only API test. It calls `setActivePinia(createPinia())`, stubs `fetch` with `vi.stubGlobal` and returns real `Response` objects. Problem responses use `application/problem+json`. Clean up with `vi.unstubAllGlobals()` and reset the `apiEvents` callbacks in `afterEach`.
- No spec mounts a component. `@vue/test-utils` and `@pinia/testing` are installed but no spec imports them.

## Docs

- `docs/architecture.md`, section "Web admin structure", describes the session, setup, router and i18n design. Update it in the same PR when you change those flows.
- `docs/features/household.md` covers the admin's scope (the web admin has every feature; the parent app has daily operations).
