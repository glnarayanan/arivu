# Contributing to Arivu

Thanks for your interest in contributing. This repository contains the standalone Go implementation of Arivu.

## Local Development

Use Go 1.24 or newer.

```bash
git clone https://github.com/glnarayanan/arivu.git
cd arivu
go run ./cmd/arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

Open `http://127.0.0.1:8080/auth`.

For production-like local behavior, set:

```bash
SECRET_KEY=replace-with-at-least-32-random-bytes
APP_URL=http://127.0.0.1:8080
COOKIE_SECURE=false
```

## Architecture Notes

- The app is a single Go module with the server, embedded frontend, workers, CLI commands, and migration tooling.
- The shipped frontend lives under `internal/app/web` and should remain dependency-free.
- SQLite is the persistence layer. Keep foreign keys, WAL behavior, and user-scoped predicates intact.
- Web, CLI, and extension sessions are audience-scoped. Do not let non-web tokens reach web or admin handlers.
- Server-side outbound fetches must use `internal/safefetch` so SSRF controls stay centralized.
- Keep long-form docs under `documentation/`; keep root docs limited to community-standard entry points.

## Making Changes

1. Fork the repository.
2. Create a feature branch: `git checkout -b feat/your-feature`.
3. Follow existing package boundaries and keep changes narrowly scoped.
4. Run formatting and tests.
5. Submit a pull request with a clear description and test evidence.

## Commit Messages

Use Conventional Commit style:

```text
type(scope): description
```

Examples:

- `feat(bookmarks): add collection filter`
- `fix(auth): enforce extension token audience`
- `docs: update migration guide`

## Code Style

- Run `gofmt` on changed Go files.
- Prefer the Go standard library and existing local helpers over new dependencies.
- Do not add a new dependency without explaining why the standard library or existing code is insufficient.
- Keep user-scoped data access filtered by user ID unless the route is explicitly admin-only.
- Keep provider integrations as direct HTTP clients unless there is a strong reason to add an SDK.

## Testing

```bash
GOCACHE=/private/tmp/arivu-build-cache go test ./...
GOCACHE=/private/tmp/arivu-build-cache go build -trimpath -ldflags="-s -w" -o /private/tmp/arivu-check ./cmd/arivu
```

If you change deployment files, also review `documentation/deployment.md`.

If you change security-sensitive code, update `SECURITY.md` or `documentation/security-model.md` when the operating model changes.

## Questions

Open a [GitHub issue](https://github.com/glnarayanan/arivu/issues) for questions, bugs, and feature discussions. Do not open public issues for security vulnerabilities; follow `SECURITY.md`.
