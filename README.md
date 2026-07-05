# Arivu

AI-native bookmarking, rebuilt as a low-dependency single binary.

Arivu is a self-hosted bookmarking app with an embedded browser UI, SQLite persistence, durable background jobs, direct provider integrations, a small CLI, and migration tooling for legacy Arivu exports.

## What Ships

- One Go binary for the web app, API, workers, CLI commands, and migration tooling.
- Embedded dependency-free frontend assets served from the binary.
- SQLite with WAL, foreign keys, optional FTS5, and a supported LIKE search fallback.
- Opaque web, CLI, and extension sessions with audience isolation and CSRF protection for web mutations.
- SSRF-safe outbound fetching, backend-owned HTML sanitization, and direct HTTP clients for Gemini, Resend, and X.
- Browser extension support through extension-scoped API routes.
- Second-brain workflow surfaces for Inbox triage, Focus tasks/reminders,
  Review, standalone notes, explicit links, annotations, cited answers, and
  Obsidian-ready exports.

## Quick Start

Requires Go 1.24 or newer.

```bash
go run ./cmd/arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

Open `http://127.0.0.1:8080/auth`.

For production, run behind TLS and set at least:

```bash
SECRET_KEY=replace-with-at-least-32-random-bytes
APP_URL=https://arivu.example.com
COOKIE_SECURE=true
ADMIN_EMAILS=admin@example.com
SIGNUPS_ENABLED=false
```

## Build

```bash
go test ./...
go build -trimpath -ldflags="-s -w" -o arivu ./cmd/arivu
./arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

## Forks

Arivu keeps `github.com/glnarayanan/arivu` as the canonical Go module path, so internal imports match the upstream module. Forks can build normally without renaming those imports; only rename the module path if the fork becomes a separate long-lived distribution. Runtime outbound fetches use the neutral `Arivu/2.0` user agent by default and can be branded with `ARIVU_FETCH_USER_AGENT`.

## Docker

```bash
docker build -t arivu:local .
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -v arivu-data:/data \
  -e ARIVU_DB=/data/arivu.sqlite3 \
  -e SECRET_KEY=replace-with-at-least-32-random-bytes \
  -e APP_URL=https://arivu.example.com \
  -e COOKIE_SECURE=true \
  arivu:local
```

A Compose sample is available at `deploy/compose.yaml`.

## Migration

Use `arivu migrate` with a validated legacy JSON export. See `openwiki/domain/migration-guide.md`.

## Documentation

Start with `openwiki/user-guide.md` if you are running Arivu, or
`openwiki/quickstart.md` if you are changing the codebase. OpenWiki contains the
user guide, codebase guide, deployment notes, dependency policy, security model,
schema reference, GitHub Wiki publishing notes, and legacy migration guide.

## Legacy Repository

The previous Python/FastAPI/MongoDB/React implementation is archived separately as `arivu-legacy`. This repository is the canonical low-dependency Go implementation.
