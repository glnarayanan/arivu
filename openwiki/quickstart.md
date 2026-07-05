# OpenWiki Quickstart

Welcome to **Arivu**, a secure, self-hosted bookmarking, semantic knowledge graph, and search engine. Arivu has been completely refactored from a multi-service Python/FastAPI/React application into a low-dependency, high-performance, single-binary Go application with SQLite persistence and an embedded dependency-free web frontend.

This directory contains the project documentation for developers and AI
assistants.

---

## Navigating the Wiki

Use the following section pages to explore specific areas of the repository:

- **[Runtime & Storage](architecture/runtime.md)**: web server architecture, middleware, SQLite pragmas, schema, and jobs.
- **[Security Model](workflows/auth-security.md)**: opaque token sessions, cookie audience protection, CSRF, SSRF prevention, and backend-owned HTML sanitization.
- **[Frontend & Extensions](architecture/frontend.md)**: embedded UI, WebExtension layout, CLI client, and asset caching.
- **[Frontend Runtime](architecture/frontend-runtime.md)**: browser runtime rules, route behavior, and smoke checks.
- **[Domain & Processing Workflows](domain/bookmarks-sync.md)**: semantic graph generation, collections, duplicate grouping, and direct-HTTP providers.
- **[Legacy Migration](domain/migration.md)**: legacy JSON export parser, Fernet secret decryption, validation rules, and SQLite ingestion.
- **[Migration Guide](domain/migration-guide.md)**: command reference and cutover guarantees.
- **[Deployment](operations/deployment.md)**: local, container, and systemd deployment.
- **[Dependency Policy](operations/dependency-policy.md)**: accepted dependencies and supply-chain checks.
- **[SQLite Schema](reference/sqlite-schema.md)**: persistence model and FTS fallback.
- **[Testing Guidelines](testing/tactics.md)**: Go tests, golden fixtures, browser smoke checks, and future-change precautions.

---

## Running and Developing

Arivu runs completely from a single compiled binary without complex runtime dependencies (no `nodejs` or `npm` required in production).

### Local Verification

Validate changes locally using Go's built-in testing tools:

```bash
# Run unit and golden-fixture tests
go test ./...

# Build the release binary
go build -trimpath -ldflags="-s -w" -o ./arivu ./cmd/arivu
```

### CLI Commands

The `arivu` command suite acts as both a web server and a local CLI manager:

```bash
# Serve the web client and backend API
./arivu serve --addr :8080 --db arivu.sqlite3

# Validate a legacy JSON export
./arivu migrate --json-export /path/to/legacy-export --out migration-manifest.json --dry-run

# CLI login
./arivu login --email you@example.com --password 'replace-me'

# Add, list, or search bookmarks directly from bash
./arivu save "https://go.dev"
./arivu list
./arivu search "SSRF mitigation"
```

---

## High-Level Codebase Layout

```
|-- cmd/arivu/                  # CLI and server entrypoint
|-- internal/
|   |-- app/                    # HTTP handlers, routers, and embedded frontend (web/)
|   |-- auth/                   # Identity, tokens, and audience enforcement
|   |-- bookmarks/              # Analytics, imports, knowledge graph, and summaries
|   |-- config/                 # Typed environment configurations
|   |-- database/               # SQLite client, schema, migration pragmas
|   |-- ids/                    # Cryptographically secure random ID generators
|   |-- jobs/                   # Durable SQLite background queue and worker loops
|   |-- migrate/                # Legacy Fernet-decryption and data ingesters
|   |-- providers/              # Direct API wrappers for Gemini, Resend, and X
|   |-- runtimeconfig/          # Runtime provider settings from SQLite/env
|   |-- safefetch/              # SSRF-shielded outbound HTTP client
|   |-- sanitize/               # Restrictive HTML sanitizer
|   `-- secrets/                # Secret encryption helpers
|-- extension/                  # Companion browser extension (vanilla JS)
`-- deploy/                     # Docker Compose, systemd, and env templates
```
