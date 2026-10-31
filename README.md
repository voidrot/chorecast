# chorecast

Self hosted chore and behavior system.

- **server/** — Go API server (chi + Huma, SQLite) that also serves the web admin
- **web/** — Vue 3 + Naive UI + Tailwind admin SPA, embedded into the server binary
- **android/** — Kotlin + Jetpack Compose app for parents, kids and shared family tablets

See [docs/architecture.md](docs/architecture.md) for the stack and auth decisions.

## Development

Everything is driven by [mise](https://mise.jdx.dev):

```sh
mise install          # Go, Node, pnpm, JDK, Android cmdline-tools, sqlc, goose, linters, lefthook
mise run setup        # deps + git hooks
mise run dev          # server on :8080, Vite on :5173
mise run gen          # sqlc + OpenAPI spec + TS client
mise run ci           # lint, test, build (server + web)

mise run android:sdk  # one-time: SDK platform + build tools (accept licenses)
mise run build:android
```

Commits follow [Conventional Commits](https://www.conventionalcommits.org); releases are cut by release-please.

## Self-hosting

```sh
docker compose -f deploy/compose.yaml up -d
```
