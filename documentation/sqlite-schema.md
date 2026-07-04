# SQLite Schema Notes

The Go rewrite normalizes the legacy Mongo document model into SQLite tables
with foreign keys.

## Core Tables

- `users`: account identity, password hash scheme, ban/invite state.
- `sessions`: hashed opaque access/refresh tokens by audience.
- `password_reset_tokens`: hashed reset tokens with expiry and use tracking.
- `bookmarks`: URL, metadata, sanitized archived HTML, text, read state, X metadata, embedding metadata, resurfacing fields.
- `ai_summaries`: one row per bookmark summary.
- `collections` and `collection_bookmarks`: normalized collection membership.
- `bookmark_accesses`: access history.
- `bookmark_entities` and `bookmark_concepts`: normalized graph terms.
- `notes` and `bookmark_notes`: standalone Markdown/plain-text notes plus optional bookmark links.
- `annotations`: quoted text, user notes, selector metadata, and optional tag labels attached to bookmarks.
- `tags`, `tag_aliases`, and `bookmark_tags`: normalized per-user tags with aliases so provider suggestions and manual tags converge instead of creating synonym clutter.
- `saved_searches`: named user searches with structured filter JSON.
- `review_events`: review completion and snooze history for bookmarks and notes.
- `import_sources`: source metadata for migration/import reports.
- `import_jobs`: user-facing import progress.
- `x_connections` and `oauth_states`: provider connection and PKCE state.
- `settings`: encrypted or plain runtime settings with key IDs.
- `rate_limits`, `audit_events`, and `jobs`: local operational state.

## Second-Brain Defaults

- AI fields are nullable. Saves, notes, annotations, tags, search, graph, review,
  and exports must keep working without a Gemini key.
- New saves create an `ai_summaries` placeholder and a visible background job.
  When processing succeeds, deterministic local extraction fills summary bullets,
  highlight quotes, suggested tags, graph entities, and graph concepts. Gemini
  embeddings are added only when configured.
- Tags are user-scoped and normalized by slug. Aliases are also user-scoped and
  unique by normalized alias slug.
- Annotation selectors and saved-search filters are stored as bounded JSON
  objects; arbitrary arrays or scalar JSON values are rejected at the API layer.
- Imports use the same `safefetch` URL validation policy as normal saves before
  inserting any bookmark or queuing a fetch job.

## FTS5

The schema attempts to create a `bookmarks_fts` virtual table. If the local
SQLite build lacks FTS5, startup continues and search falls back to `LIKE`.
This fallback is a supported production mode for the first rewrite release so
operators are not forced into a custom SQLite build. Production builds should
still prefer FTS5 when available because it leaves room for lower-latency
full-text ranking, but FTS5 is not a hard runtime requirement.
