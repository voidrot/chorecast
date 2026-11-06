# Contributing to Chorecast

Thank you for your interest in contributing to Chorecast.

Chorecast is an open-source, self-hosted chore and behavior management system for families. Contributions that improve reliability, security, accessibility, usability, documentation, and maintainability are welcome.

This guide explains how to contribute and what maintainers expect from submitted changes.

## Before You Contribute

Please review:

- [README.md](README.md) — Project overview and setup.
- [Architecture](docs/architecture.md) — Technical decisions and system design.
- [GOVERNANCE.md](GOVERNANCE.md) — Project governance and maintainer responsibilities.
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Community expectations.
- [SECURITY.md](SECURITY.md) — Private vulnerability reporting.

**Security vulnerabilities must not be disclosed in public issues or pull requests.** Follow the instructions in SECURITY.md instead.

## 1. Ways to Contribute

Contributions may include:

- Bug fixes and regression tests.
- Improvements to existing functionality.
- Performance and reliability improvements.
- Documentation and installation guidance.
- Accessibility and user experience improvements.
- Security hardening.
- Tests and development tooling.
- Compatibility improvements across supported platforms.

For substantial new features, breaking changes, or architectural changes, open an issue to discuss the approach before beginning implementation.

This helps avoid work that conflicts with existing design decisions or planned development.

## 2. Development Environment

Chorecast is organized into several components.

| Directory | Technology | Purpose |
|---|---|---|
| `server/` | Go, chi, Huma, SQLite, sqlc | API and application server |
| `web/` | Vue 3, TypeScript, Vite | Web administration interface |
| `android/` | Kotlin, Jetpack Compose | Android application |
| `api/` | OpenAPI | Generated API contracts |
| `deploy/` | Docker | Container builds and deployment |
| `docs/` | Markdown | Documentation |

### Prerequisites

Install:

- Git
- [mise](https://mise.jdx.dev/)
- Docker with Compose v2 for container workflows
- An Android SDK environment for Android development

Most development tools and versions are managed through `mise.toml`.

### Clone and Set Up

```bash
git clone https://github.com/voidrot/chorecast.git
cd chorecast

mise install
mise run setup
```

The setup task installs project dependencies and configures Git hooks.

### Start Development Services

```bash
mise run dev
```

The development environment runs:

- Go API server on `http://localhost:8080`.
- Vite development server on `http://localhost:5173`.

The Vite development server proxies API requests to the Go server.

### Available Tasks

```bash
mise tasks
```

Common tasks include:

| Command | Purpose |
|---|---|
| `mise run dev` | Run development services |
| `mise run gen` | Regenerate SQL and API clients |
| `mise run fmt` | Format code |
| `mise run lint` | Run server and web linters |
| `mise run test` | Run server and web tests |
| `mise run build` | Build server and web |
| `mise run ci` | Run primary CI validation |
| `mise run build:android` | Build Android |
| `mise run test:android` | Run Android tests |
| `mise run lint:android` | Run Android lint |

Run the relevant checks before submitting a pull request.

## 3. Branching Strategy

Create a focused branch from the latest `main`.

Branch names should use the same general categories as Conventional Commits.

Examples:

```text
feat/recurring-chore-rules
fix/device-pairing-timeout
docs/self-hosting-guide
refactor/server-auth
test/points-ledger
ci/android-build
```

Keep branches focused on a single logical change whenever possible.

## 4. Coding Guidelines

### General Principles

- Follow established architecture and patterns.
- Prefer straightforward solutions over unnecessary abstraction.
- Keep changes small and reviewable.
- Avoid introducing dependencies without clear justification.
- Preserve backward compatibility where required.
- Add or update tests for behavioral changes.
- Update documentation when behavior or configuration changes.
- Avoid unrelated formatting or refactoring.

### Go Server

- Follow standard Go conventions and use `gofmt`.
- Use the existing chi and Huma API patterns.
- Keep business logic in the appropriate internal packages.
- Use typed, generated sqlc queries for database operations.
- Preserve authentication and authorization enforcement.
- Handle errors explicitly and consistently.
- Use context-aware operations where appropriate.
- Ensure background jobs remain safe under retries.

Every API operation must explicitly declare an authentication policy using the existing public or scoped authorization mechanisms.

Operations that access child-specific information must enforce the appropriate data-access checks in addition to operation-level scopes.

### Web Application

- Use Vue 3 and TypeScript conventions already established in the project.
- Follow existing component and composable patterns.
- Prefer generated API types rather than manually duplicating contracts.
- Follow existing styling and UI component conventions.
- Keep client-side validation consistent with server behavior.
- Do not rely on frontend authorization checks as a replacement for server-side enforcement.

### Android Application

- Follow Kotlin and Jetpack Compose conventions.
- Use existing application architecture and shared modules.
- Preserve secure credential handling.
- Avoid storing authentication secrets in unprotected storage.
- Maintain compatibility with the supported `play` and `foss` variants.
- Consider shared-device behavior and session isolation.
- Update generated API integrations when server contracts change.

### Database

Chorecast uses SQLite, embedded migrations, and sqlc.

Modify SQL source files and migrations rather than editing generated sqlc output directly.

Follow the current migration policy documented in the repository.

Before version 1.0, schema compatibility may change between releases. Do not assume forward-only migrations are already in effect.

## 5. Generated Code

Chorecast generates and commits several API-related artifacts.

These include:

- `server/internal/db/sqlc/`
- `api/openapi.json`
- `api/openapi-3.0.json`
- `web/src/api/schema.d.ts`

After changing SQL queries, database schemas, or API operations, run:

```bash
mise run gen
```

Commit all relevant generated changes.

CI checks generated artifacts for drift and may reject pull requests containing outdated generated code.

The Android API client is generated at build time and should not be committed unless the repository's generation policy changes.

Do not manually edit generated files.

## 6. Testing and Validation

Contributors should run applicable tests before submitting changes.

### Standard Validation

```bash
mise run fmt
mise run lint
mise run test
mise run ci
```

### Go Tests

```bash
cd server
go test ./...
go test -race ./...
```

### Web Tests

```bash
mise run test:web
```

### Android Validation

```bash
mise run test:android
mise run lint:android
mise run build:android
```

Not every change requires every platform to be rebuilt locally, but contributors should validate affected components.

CI provides additional validation and must pass before a contribution is considered ready for merging.

### Test Expectations

- Bug fixes should include regression tests when practical.
- New behavior should include appropriate tests.
- Authorization changes should test permitted and denied access.
- API changes should account for relevant compatibility behavior.
- Tests should be deterministic and independent where practical.
- Use synthetic data, not real household records.

## 7. Commit Messages

Chorecast uses [Conventional Commits](https://www.conventionalcommits.org/).

Format:

```text
type(scope): short description
```

Common types:

| Type | Purpose |
|---|---|
| `feat` | New functionality |
| `fix` | Bug fix |
| `docs` | Documentation |
| `refactor` | Internal restructuring |
| `perf` | Performance improvement |
| `test` | Tests |
| `build` | Build system changes |
| `ci` | CI/CD changes |
| `chore` | Maintenance |
| `style` | Formatting or nonfunctional style changes |
| `revert` | Revert a previous change |

Examples:

```text
feat(server): add recurring chore support
fix(android): prevent stale device sessions
docs: expand installation instructions
test(server): cover unauthorized kid access
chore(deps): update Go dependencies
```

Scopes are optional.

Breaking changes should use Conventional Commit breaking-change notation.

Git hooks validate commit messages, and release automation uses Conventional Commits to determine version changes.

## 8. Pull Requests

Chorecast uses squash merging.

The **pull request title becomes the squash commit title** and must follow Conventional Commit conventions.

Before submitting:

1. Rebase or update your branch against `main` as appropriate.
2. Ensure the change is focused and documented.
3. Run applicable formatting, linting, and tests.
4. Regenerate and commit derived artifacts when required.
5. Review the diff for accidental secrets or unrelated changes.
6. Submit a pull request with a descriptive title and summary.

### Pull Request Description

Include:

- What changed.
- Why the change is necessary.
- How it was tested.
- Relevant issue references.
- Any compatibility or migration implications.
- Screenshots for meaningful UI changes, when useful.

A suggested format:

```markdown
## Summary

Describe the change and its motivation.

## Changes

Describe the major implementation changes.

## Testing

Describe the tests and validation performed.

## Compatibility

Note API, database, deployment, or client compatibility concerns.

## Related Issues

Closes #123
```

### Review Process

Pull requests are reviewed by the maintainers identified in [.github/CODEOWNERS](.github/CODEOWNERS).

Maintainers may request additional testing, documentation, or implementation changes.

Security-sensitive contributions may require additional scrutiny.

Approval and merging are at maintainer discretion.

## 9. Security Requirements

Security is particularly important because Chorecast handles sensitive family information.

Contributors must:

- Preserve authentication and authorization checks.
- Enforce child-specific data access restrictions.
- Avoid exposing sensitive information in logs or API responses.
- Protect credential and session handling.
- Validate untrusted input.
- Handle uploaded media securely.
- Avoid introducing insecure deployment defaults.
- Protect build and release credentials.
- Review dependency changes for security implications.

Do not commit passwords, API keys, private keys, signing credentials, database files, or production secrets.

If you discover a vulnerability, report it privately according to [SECURITY.md](SECURITY.md).

## 10. Privacy and Test Data

Do not include real household data in contributions.

This includes:

- Children's names or birth dates.
- Household photographs.
- Private behavior or activity records.
- Authentication tokens.
- Production databases or backup archives.
- Personally identifiable information from actual users.

Use synthetic fixtures and anonymized examples.

Tests involving children's profiles or household behavior must not require real personal information.

See [PRIVACY.md](PRIVACY.md) for the project's privacy principles.

## 11. Documentation Contributions

Documentation improvements are welcome.

Use clear, direct language and keep instructions consistent with current implementation.

When changing application functionality, update the relevant documentation in the same pull request where practical.

Examples include:

- Configuration changes.
- New API functionality.
- Changes to authentication behavior.
- Deployment or upgrade procedures.
- Database compatibility changes.
- User-facing workflows.

## 12. Dependency Changes

Dependency updates should follow the project's existing tooling and release policies.

Chorecast uses Renovate to manage dependency updates.

Before introducing a new dependency, consider:

- Maintenance activity and long-term viability.
- License compatibility.
- Security history.
- Runtime and build impact.
- Whether existing dependencies can provide the functionality.

Avoid introducing dependencies for functionality that can be implemented clearly using existing tools.

## 13. Licensing

Chorecast is licensed under the [GNU Affero General Public License v3.0](LICENSE).

By contributing, you agree that your contribution may be distributed under the project's existing license.

Contributors must have the right to submit their work and must not include third-party material that cannot legally be distributed under the applicable licensing terms.

## 14. Getting Help

For ordinary development questions or feature proposals, use the repository's [GitHub Issues](https://github.com/voidrot/chorecast/issues).

For security vulnerabilities, follow [SECURITY.md](SECURITY.md).

For community conduct concerns, follow [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

---

Contributions are reviewed under the project's maintainer-led governance model described in [GOVERNANCE.md](GOVERNANCE.md).