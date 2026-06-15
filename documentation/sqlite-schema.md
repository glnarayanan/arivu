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
- `import_jobs`: user-facing import progress.
- `x_connections` and `oauth_states`: provider connection and PKCE state.
- `settings`: encrypted or plain runtime settings with key IDs.
- `rate_limits`, `audit_events`, and `jobs`: local operational state.

## FTS5

The schema attempts to create a `bookmarks_fts` virtual table. If the local
SQLite build lacks FTS5, startup continues and search falls back to `LIKE`.
This fallback is a supported production mode for the first rewrite release so
operators are not forced into a custom SQLite build. Production builds should
still prefer FTS5 when available because it leaves room for lower-latency
full-text ranking, but FTS5 is not a hard runtime requirement.
