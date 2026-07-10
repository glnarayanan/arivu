# SQLite Schema Notes

The Go rewrite normalizes the legacy document model into SQLite tables
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
- `daily_notes`: one dated planning note per user and calendar day for the Today cockpit.
- `annotations`: quoted text, user notes, selector metadata, and optional tag labels attached to bookmarks.
- `tags`, `tag_aliases`, and `bookmark_tags`: normalized per-user tags with aliases so provider suggestions and manual tags converge instead of creating synonym clutter.
- `saved_searches`: named user searches with structured filter JSON.
- `review_events`: review completion and snooze history for bookmarks and notes.
- `item_states`: per-user processing stage, priority, and next action for bookmark
  and note inbox workflows.
- `item_links`: explicit per-user bookmark/note relationships with labels and
  source provenance.
- `search_index`: durable per-user retrieval rows for bookmarks and notes,
  including normalized title, body, tags, link text, source, and update time.
- `search_fts`: optional FTS5 mirror of `search_index` used for ranked
  full-text retrieval when the local SQLite build supports FTS5.
- `result_feedback`: per-user recall feedback for bookmark/note results across
  search, cited answers, and review.
- `reminders`: due reminders for bookmarks and notes with stored timezone,
  recurrence, notification channel, last-notified, and last-completed state.
- `action_items`: durable completable tasks attached to bookmarks or notes.
- `assistant_actions`: per-user proposal ledger for allowlisted assistant
  mutations with pending, executed, rejected, and failed statuses.
- `import_sources`: source metadata for migration/import reports, grouped into
  per-import source reports for the Settings import status view.
- `import_jobs`: user-facing import progress with fetched, AI-processed,
  failed, and completed status counters.
- `x_connections` and `oauth_states`: provider connection and PKCE state.
- `settings`: encrypted or plain runtime settings with key IDs.
- `rate_limits`, `audit_events`, and `jobs`: local operational state.

## Second-Brain Defaults

- AI fields are nullable. Saves, notes, annotations, tags, search, graph, review,
  and exports must keep working without a configured model-provider key.
- New saves create an `ai_summaries` placeholder and a visible background job.
  A successful provider summary owns its structured summary, long form, bullets,
  highlights, and suggested tags; deterministic local extraction fills those
  display fields only as a fallback and always supplies graph entities and
  concepts. Gemini embeddings use `gemini-embedding-2` when Gemini is selected
  and configured.
- New bookmarks and notes enter `item_states` as `inbox`. Users can move them
  through `processing`, `processed`, and `archived` without changing the source
  bookmark or note content.
- `item_links` is polymorphic, so API handlers must validate that both endpoints
  belong to the authenticated user before inserts or reads expose relationship
  metadata.
- Reminder due times are stored as UTC RFC3339 strings while `timezone`
  preserves the user's intended local clock for recurring schedules.
  `recurrence` supports `none`, `daily`, `weekly`, `monthly`, and bounded
  custom day intervals. API handlers validate item ownership before creating,
  updating, snoozing, completing, deleting, exporting, or restoring reminder
  rows.
- Reminder notifications are always visible in-app through computed `due_state`
  and `is_due` response fields. Rows with `notification_channel='email'` queue a
  scheduled `reminder.email` job. Email delivery is idempotent through the
  reminder ID, exact `due_at`, and `last_notified_at`.
- `action_items` stores multiple concrete tasks per bookmark or note. Because
  item targets are polymorphic, handlers and restore code validate target
  ownership before inserts, and bookmark/note deletion explicitly removes their
  action items.
- `assistant_actions` stores bounded JSON payloads for allowlisted mutations:
  updating item state, creating explicit links, creating reminders, and creating
  action items. Proposal and approval both validate ownership; stale proposals
  are marked `failed` instead of executing.
- Tags are user-scoped and normalized by slug. Aliases are also user-scoped and
  unique by normalized alias slug.
- Annotation selectors and saved-search filters are stored as bounded JSON
  objects; arbitrary arrays or scalar JSON values are rejected at the API layer.
- Bookmark list/search filters are user-scoped and support domain, source,
  read status, created-date bounds, normalized tag names, and tag aliases.
  Legacy bookmark list search still uses the bookmark filter path.
- Unified retrieval uses `search_index` for both bookmarks and notes. Bookmark
  rows include URL/domain metadata, descriptions, archived text, AI summaries,
  annotations, linked notes, tags, and explicit graph-link text. Note rows
  include note title/body plus outgoing and incoming graph-link text.
- `/api/search/items` is read-only and reads the maintained index. `/api/search/rebuild`
  is the quota-protected repair path for imports or operator repair and is the
  only web route that rebuilds search rows directly.
- Search and cited-answer results include `why_shown`, `freshness_score`, and
  `feedback_state` metadata. `POST /api/feedback` stores user-scoped result
  feedback; `never_resurface` suppresses future Review appearances without
  deleting the source item.
- Imports use the same `safefetch` URL validation policy as normal saves before
  inserting any bookmark or queuing a fetch job. Queued import bookmark jobs
  carry the owning import job ID so background processing can update progress
  without crossing user boundaries.
- `rate_limits` is shared by public auth throttles and authenticated mutation
  quotas. Mutation quota keys are SHA-256 hashes of a namespace, audience,
  user ID, and quota name, so raw user IDs are not stored in the key column.

## FTS5

The schema attempts to create `bookmarks_fts` and `search_fts` virtual tables.
If the local SQLite build lacks FTS5, startup continues and search falls back to
`LIKE` over durable tables. This fallback is a supported production mode for the
first rewrite release so operators are not forced into a custom SQLite build.
Production builds should still prefer FTS5 when available because it enables
ranked full-text retrieval, but FTS5 is not a hard runtime requirement.
