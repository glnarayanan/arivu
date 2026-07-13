# Runtime & Storage Architecture

Arivu is architected as a lean, concurrent, single-binary application.

Public-share membership is an explicit publication boundary: selected evidence
and public bookmark fields are copied into snapshot columns. Public projections
never join live bookmark or evidence content. The embedded public-reader JS and
CSS are served by the normal static asset path under the default self-only CSP.

Optional browser preservation runs only after successful direct capture through
the versioned stdin/stdout helper protocol documented in the deployment guide.
Outputs enter a mode-0700 staging directory, are type/MIME/path/size checked,
SHA-256 hashed while entering the private content-addressed asset store, and are
served only through owner-authenticated artifact endpoints.

## Capture attempts and local artifacts

Direct HTTP processing records capture attempts with queued, running, complete,
partial, and failed states. The same bounded, SSRF-protected fetch that creates
evidence supplies the original response bytes without a second request. These
`source_response` artifacts live outside the web root under `<database>.assets`.
Writes use random staging files, enforce the fetch bound while hashing, fsync,
and atomically rename to SHA-256-derived object keys before metadata is stored.
Authenticated artifact APIs filter by owner and return content with `nosniff`
and `no-store`.

Rows can be made unreachable with `deleted_at`; physical garbage collection and
orphan reconciliation are grace-based maintenance operations and never remove
an object while any live row references it. Per-user artifact quota counts each
logical live reference, even when content-addressed objects are shared. Installer
backups copy adjacent live assets (excluding `.staging`) and verify a manifest.
The bounded startup/hourly pass materializes all live keys before walking files
(avoiding nested work on single-connection SQLite); `ARIVU_ASSET_GC_GRACE`
defaults to 24 hours. Missing referenced files are logged without metadata loss.

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

On open, `Migrate()` converges additive SQLite schemas in three phases: create
tables and unique indexes from `schema.sql`, apply `ensure*` column and
backfill helpers for existing installs, then create non-unique indexes. That
order keeps upgrades safe when new indexes reference columns that only exist
after `ALTER TABLE` ensures.

The full tables structure is declared in `/internal/database/schema.sql`. It models:
- **Core Entities**: `users`, `sessions`, `bookmarks`, `collections`, `collection_bookmarks` (join table).
- **Processing Outputs**: `ai_summaries` (from the configured model provider), `bookmark_entities`, and `bookmark_concepts`.
- **Evidence & Repair**: `bookmark_evidence` preserves versioned source
  contexts; `quality_reprocess_runs` and `quality_reprocess_items` track
  explicitly scoped, backup-verified repair batches.
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

RSS polling is a durable `feed.poll` job. Due rows are materialized and their
cursor is closed before enqueue/update work, which keeps the single-connection
SQLite configuration safe from nested-query deadlocks. Polls use conditional
requests, bounded SSRF-safe fetches, capped entry batches, duplicate keys and
fingerprints, and exponential retry delay after transport failures.

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
  - Uses a four-minute worker lease, sized for the longest bounded bookmark
    path: up to 30 seconds for safe fetch, one shared 90-second summary budget
    including its validation retry, up to 30 seconds for embedding, and a
    90-second lease margin. Shorter reminder jobs share this queue lease.
  - Limits execution concurrency using bounded pools of Go workers.
  - Recovers on server restart by scanning non-completed entries.

Reminder email jobs are app-level jobs because they need runtime Resend
settings. A job is idempotent: it sends only when the payload reminder ID and
payload due time still match a pending reminder whose `last_notified_at` is
empty. Missing Resend configuration makes the job a no-op rather than failing
the core reminder workflow.

Quality reprocessing queues ordinary durable `bookmark.process` jobs in bounded
batches. Payloads carry the repair run, target versions, and selected-evidence
hash. Queueing never deletes active artifacts; validated replacements are the
processor's swap boundary.

Provider usage telemetry classifies failures into stable codes such as
`provider_timeout`, `provider_rate_limited`, and `provider_auth`. Admin APIs and
the embedded UI receive only the safe code and a bounded public message; raw
network errors, endpoint URLs, query parameters, and credentials are never
stored in process-local telemetry.
