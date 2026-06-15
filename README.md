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

Use `arivu migrate` with a validated legacy JSON export. See `documentation/migration-guide.md`.

## Documentation

- Architecture: `documentation/architecture.md`
- Deployment: `documentation/deployment.md`
- Security model: `documentation/security-model.md`
- Dependency policy: `documentation/dependency-policy.md`
- SQLite schema: `documentation/sqlite-schema.md`
- Migration guide: `documentation/migration-guide.md`
- Browser workflow checks: `documentation/browser-workflow-checks.md`

## Legacy Repository

The previous Python/FastAPI/MongoDB/React implementation is archived separately as `arivu-legacy`. This repository is the canonical low-dependency Go implementation.
