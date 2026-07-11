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

The additive knowledge reads are registered beside existing endpoints:

- `GET /api/library/items`: cursor-based unified user library.
- `GET /api/knowledge-graph/v2`: bounded typed graph with optional focus/depth.
- `GET /api/insights`: deterministic evidence-backed patterns.
- `POST /api/feedback`: retains existing result feedback and additionally
  accepts server-validated insight/relationship targets.

All are wrapped in the existing web-audience authentication. Feedback remains a
normal CSRF-protected mutation.

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
- **Processing Outputs**: `ai_summaries` (from the configured model provider), `bookmark_entities`, and `bookmark_concepts`.
- **Integrations**: `import_jobs`, `x_connections`, `oauth_states`.
- **Durable Controls**: `settings` (encrypted provider secrets plus plain runtime settings), `rate_limits`, `audit_events`, and `jobs`.
- **Knowledge Feedback**: `knowledge_feedback` stores user-scoped insight and
  relationship feedback. Library rows, graph nodes/edges, and insights are
  read-time projections rather than duplicate durable content tables.

The current knowledge redesign needs no blocking startup backfill. Existing
bookmarks, notes, daily notes, annotations, objects, entities, concepts, links,
and embeddings become visible through projections immediately. Missing
embeddings omit only semantic-similarity edges.

`internal/runtimeconfig` resolves provider settings with this precedence:
SQLite override, environment variable, then default. Admin API key updates write
to `settings`; model providers, Resend, and X read effective values at runtime.
Text generation uses `ai_provider`, encrypted `ai_api_key`, `ai_model`, and
`ai_base_url`. Legacy `GEMINI_API_KEY`, `GEMINI_MODEL`, `GEMINI_BASE_URL`, and
the older `gemini_*` SQLite keys remain fallbacks when the selected provider is
Gemini so current installs keep working. Remote model-provider endpoints must
use HTTPS; plain HTTP is accepted only for localhost development/test endpoints
such as LM Studio or Ollama. Provider changes validate and commit the provider,
model, key, and Base URL together. LM Studio, Ollama/local, and Custom may run
without an API key; other provider changes require a replacement key so a
credential is never silently reused across providers.

---

## Durable Background Jobs Engine

Asynchronous workflows (e.g., crawling bookmarks, querying LLMs, syncing X timelines, and due reminder email notifications) are powered by a SQLite-backed task queue.

- **Queue Schema**: Scheduled items are tracked persistently in the `jobs` table.
- **Worker Pools**: Handled in `/internal/app/workers.go` and managed by `/internal/jobs/jobs.go`.
- **Properties**:
  - Keeps progress status (`queued`, `leased`, `completed`, `failed`).
  - Supports delayed jobs through `run_after`; reminder email jobs are scheduled
    for the reminder's current UTC due time.
  - Recovers expired leases after worker crashes, while completion and failure
    updates are fenced to the active `leased_until` value so stale workers cannot
    overwrite a newer lease.
  - Limits execution concurrency using bounded pools of Go workers.
  - Recovers on server restart by scanning non-completed entries.

Reminder email jobs are app-level jobs because they need runtime Resend
settings. A job is idempotent: it sends only when the payload reminder ID and
payload due time still match a pending reminder whose `last_notified_at` is
empty. Missing Resend configuration makes the job a no-op rather than failing
the core reminder workflow.
