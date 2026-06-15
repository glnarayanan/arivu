# Architecture

Arivu is a standalone low-dependency Go single-binary app with embedded frontend assets, SQLite persistence, direct provider clients, CLI commands, background workers, and legacy migration tooling.

## Runtime

- `cmd/arivu`: one binary with server, migration, and CLI commands.
- `internal/app`: HTTP routes, middleware, embedded frontend, admin endpoints, and workers.
- `internal/database`: SQLite opening, pragmas, schema initialization, WAL, and foreign keys.
- `internal/auth`: opaque web, CLI, and extension sessions with audience enforcement.
- `internal/bookmarks`: bookmarks, collections, search, analytics, graph, resurfacing, import, and export behavior.
- `internal/jobs`: durable SQLite job queue.
- `internal/safefetch`: SSRF-aware outbound content fetch.
- `internal/sanitize`: backend-owned archived HTML sanitizer.
- `internal/providers`: direct HTTP clients for Gemini, Resend, and X.
- `internal/migrate`: legacy export validation, migration manifest generation, and SQLite import executor.

## Request Flow

1. `net/http` receives requests with explicit server timeouts.
2. Middleware applies body limits, security headers, panic recovery, and request logging.
3. Authenticated API routes validate opaque access tokens, enforce session audience, and require CSRF headers for cookie-authenticated mutations.
4. Data is read/written through SQLite with foreign keys and WAL enabled.
5. Long-running content work is queued in SQLite and processed by bounded workers.

## Frontend

The browser app is embedded from `internal/app/web`. It uses first-party JavaScript modules, CSS, native browser APIs, and local assets. There is no npm runtime tree for the shipped app.

## Data And Search

SQLite stores users, sessions, bookmarks, summaries, collections, access history, entities, concepts, import jobs, X connections, OAuth state, settings, rate limits, audit events, and durable jobs. FTS5 is preferred when available; the supported fallback is SQLite `LIKE` search.

## Migration

Migration tooling validates legacy JSON exports, rejects unknown fields, preserves valid IDs, checks relationships, sanitizes archived HTML, validates embeddings, decrypts legacy Fernet-encrypted secrets with the old `SECRET_KEY`, and re-encrypts runtime settings for the new SQLite model.
