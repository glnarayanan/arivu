# Arivu

Self-hosted AI second brain for web research, notes, tasks, and review.

Arivu helps you save web pages, turn them into useful knowledge, search by
meaning, and bring important material back before it disappears into the
archive. It runs on your own infrastructure with a Go application, SQLite
persistence, embedded browser UI, browser extension, CLI, and optional AI
provider integrations.

## What Arivu Does

- Capture pages from the dashboard, browser extension, PWA share target, or
  `arivu save`.
- Triage saved pages and notes through Inbox, Working, Kept, and Archived.
- Work from Focus when an item carries a task or reminder.
- Use Review to resurface older, due, high-priority, or still-actionable items.
- Write standalone notes, link notes to saved pages, and keep backlinks.
- Search saved content by title, URL, tags, filters, archived text, and meaning
  when semantic features are available.
- Ask cited questions against your own saved content and approve assistant
  drafts before they change anything.
- Import from common bookmark tools and export JSON, CSV, browser HTML,
  Markdown, and Obsidian-ready ZIP archives.

## How It Runs

- One Go binary serves the web app, API, workers, CLI commands, and migration
  tooling.
- Embedded dependency-free frontend assets are served from the binary.
- SQLite stores content, sessions, jobs, settings, search indexes, tasks, and
  reminders.
- Web, CLI, and extension sessions are audience-isolated, with CSRF protection
  for browser mutations.
- Outbound fetching is SSRF-shielded, archived HTML is sanitized on the backend,
  and provider integrations use direct HTTP clients for Gemini, Resend, and X.

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

The user-facing guide is also published at the
[Arivu GitHub Wiki](https://github.com/glnarayanan/arivu/wiki).

## Legacy Repository

The previous Python/FastAPI/MongoDB/React implementation is archived separately as `arivu-legacy`. This repository is the canonical low-dependency Go implementation.
