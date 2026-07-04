# Runtime & Storage Architecture

Arivu is architected as a lean, concurrent, single-binary application.

---

## HTTP Runtime & Web Server

The web tier is managed directly via `/internal/app/app.go` using only the standard library (`net/http`).

### Core Server Settings

As shown in `/cmd/arivu/main.go`, the listener configures strict timeouts to prevent resource exhaust patterns or slowloris vectors:

- `ReadHeaderTimeout`: `5 * time.Second`
- `ReadTimeout`: `30 * time.Second`
- `WriteTimeout`: `60 * time.Second`
- `IdleTimeout`: `120 * time.Second`
- `MaxHeaderBytes`: `16 KB` (16 << 10)

### Route Middleware Chain

Requests pass through structured, secure middlewares configured on the main app router:

1. **Request Size Limiting**: Rejects requests wrapping bodies exceeding a strict limit to prevent heap exhaustion.
2. **Panic Recovery**: Recovers from unexpected handler panics, logging traces and responding with a structured `500 Internal Server Error`.
3. **Request Logger**: Standardized tracking of method, path, response status, duration, and client details.
4. **Security Headers**: Injects fundamental defensive headers (e.g., `X-Content-Type-Options: nosniff`, strict CSP, etc.).

---

## SQLite Database Model

Arivu persists everything to a single SQLite 3 database file. The client engine logic sits in `/internal/database/database.go`.

### Database Optimization & Reliability Pragmas

To achieve high concurrent throughput and protect data integrity, the driver initializes connections with specific SQLite pragmas:

- `PRAGMA journal_mode = WAL;` (Write-Ahead-Logging permits multiple readers and one writer concurrently).
- `PRAGMA synchronous = NORMAL;` (Reduces disk sync frequency to safe boundaries when matched with WAL).
- `PRAGMA foreign_keys = ON;` (Strict parent-child association validation on runtime writes).
- `PRAGMA busy_timeout = 5000;` (Avoids database lock failures by retrying locked files for up to 5 seconds).

### Schema Definition

The full tables structure is declared in `/internal/database/schema.sql`. It models:
- **Core Entities**: `users`, `sessions`, `bookmarks`, `collections`, `collection_bookmarks` (join table).
- **Processing Outputs**: `ai_summaries` (from Gemini), `bookmark_entities`, and `bookmark_concepts`.
- **Integrations**: `import_jobs`, `x_connections`, `oauth_states`.
- **Durable Controls**: `settings` (encrypted dynamic settings), `rate_limits`, `audit_events`, and `jobs`.

---

## Durable Background Jobs Engine

Asynchronous workflows (e.g., crawling bookmarks, querying LLMs, syncing X timelines) are powered by a SQLite-backed task queue.

- **Queue Schema**: Scheduled items are tracked persistently in the `jobs` table.
- **Worker Pools**: Handled in `/internal/app/workers.go` and managed by `/internal/jobs/jobs.go`.
- **Properties**:
  - Keeps progress status (`queued`, `leased`, `completed`, `failed`).
  - Limits execution concurrency using bounded pools of Go workers.
  - Recovers on server restart by scanning non-completed entries.
