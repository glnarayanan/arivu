# Domain & Processing Workflows

Arivu is not just a bookmark vault; it is a knowledge engine that processes URL captures, discovers duplicate materials, associates semantic tags, and maintains live timelines.

---

## Unified Knowledge Projection

Saving a bookmark initiates several processing stages to build a structured graph:

1. **Extraction**: `safefetch` validates URLs, enforces SSRF and size limits,
   removes chrome, and records complete, partial, metadata-only, or failed
   evidence with stable reasons. Source-native X evidence and linked articles
   remain separate; generic scraping cannot overwrite higher-authority text.
2. **Analysis**: Selected evidence drives bounded summaries, extractive
   highlights, and supported phrase-level semantics. Short evidence may produce
   only a sentence; metadata-only or failed evidence produces no synthetic
   claim. No-provider operation does not repopulate the graph with raw tokens.
3. **Knowledge projection**: `/api/library/items` unions bookmarks, notes, daily
   notes, annotations, knowledge objects, entities, and concepts into stable
   user-scoped rows without duplicating canonical content. Generated entities
   and concepts appear only when their confidence, enrichment version, selected
   complete evidence, and source span all pass the shared semantic gate; legacy
   token rows remain stored for audit but are quarantined from every surface.
4. **Graph relationships**: `/api/knowledge-graph/v2` projects explicit links,
   source links, shared concepts, shared entities, and semantic similarity where
   embeddings exist. Every edge has a stable ID, provenance, and confidence.
   Node and edge limits are bounded; `focus` plus depth 0-2 performs local
   expansion.
5. **Pattern detection**: `/api/insights` deterministically detects emerging
   themes, recurring connections, forgotten value, knowledge gaps, and
   serendipitous connections. Evidence joins always include the current user.

Explicit links are durable canonical knowledge. Derived graph edges and insights
are recalculated from owned source data. `knowledge_feedback` stores the durable
user response to stable insight/relationship IDs; active dismiss/snooze feedback
filters derivatives without deleting source material.

---

## Duplicate Ingest Detection

To optimize storage and prevent workspace clutter, saving a bookmark triggers a duplicate analysis algorithm:
- Bookmarks are categorized and grouped based on canonicalized web path signatures and matching title hashes.
- Suggests consolidating items into unified groups when identical content spans multiple variations.
- The web console exposes duplicate groups and can call the merge API to keep the first item while moving useful metadata from the rest.

## Capture-To-Recall Loop

Second-brain v1 adds user-authored context around bookmarks:

- Quick notes, selected quotes, and manual tags can be submitted with a new save.
- New bookmarks and notes enter the Inbox so users can decide whether each item
  should stay in Inbox, move into Working, become Kept, or be Archived from the
  working loop.
- Inbox bulk triage updates up to 100 bookmark/note items at once and returns
  partial failures for stale or cross-user targets instead of failing the whole
  operation.
- Processing state stores priority and the next action separately from the
  original bookmark or note, so review and exports preserve why an item was kept.
- Bookmark detail is reader-first, with one next-step workflow panel and
  disclosure groups for annotations, linked notes, explicit links, tasks,
  reminders, related items, and review completion.
- Bookmark detail can requeue extraction for the current URL. Reprocessing
  keeps the existing archive and generated context available while queued, then
  replaces derived content only after a successful refresh. The bookmark,
  annotations, notes, manual tags, and collection membership remain intact.
- Existing X bookmarks that predate evidence provenance are repaired during the
  next X Sync. Duplicate tweet IDs are not blindly skipped: fresh API text is
  HTML-entity decoded and retained as authoritative evidence, obviously scraped/encoded generated
  titles and descriptions are corrected, and normal processing is requeued.
  Only a current, validated summary for the same evidence hash can block a
  failed replacement; unvalidated legacy summaries are replaced by a bounded
  fallback so stale semantics cannot keep the repair job retrying indefinitely.
  X bookmarks no longer returned by the provider retain their local record and
  manual context; insufficient-evidence cleanup only decodes the stored title
  and removes generated artifacts.
- Reader annotations store text-quote selector metadata when captured from the
  sanitized page selection, so saved highlights can jump back to matching source
  text when the archive still contains that passage.
- Explicit links connect bookmarks and notes with user-authored labels. Link
  reads return outgoing and incoming relationships so a saved item can show both
  what it points to and what points back to it. Link picker target reads use
  `/api/link-targets`, returning only slim bookmark/note metadata for the
  authenticated user.
- Reminders attach due times, local timezone, recurrence, notification channel,
  and reminder notes to bookmarks or notes. Reminder reads decorate each row
  with the item title, due state, and completion/notification timestamps while
  mutations continue to validate item ownership.
- Action items attach multiple completable tasks to a bookmark or note. They are
  separate from the single `next_action` processing prompt and from dated
  reminders, so one saved item can carry a checklist without becoming a project
  management system.
- The Notes list is compact. `/notes/:id` owns note editing, action items,
  reminders, explicit note links, backlinks, note-to-note links, and
  note-to-bookmark links.
- Knowledge objects add a lightweight typed layer over saved information.
  `/api/objects` retains structured fields and optional source references; the
  normal browser composer presents type-specific native fields rather than raw
  JSON.
- `/api/calendar/import` parses pasted ICS `VEVENT` entries into meeting
  objects, preserving UID, start, end, location, description, and source fields.
- `/api/evolution` builds a topic timeline from daily notes, saved pages, notes,
  and knowledge objects so a thought can be traced across weeks or months.
- `/api/today-board` returns a fixed local board with Inbox, Working, Review,
  recent decisions, and recent meetings before any infinite canvas exists.
- The Focus page reads action items and reminders through existing APIs and
  supports pending, overdue, today, upcoming, and completed views.
- Assistant suggestions generate inert drafts from an item, search query, Inbox
  stage, or Review queue. Drafts are limited to bounded second-brain mutations:
  item state updates, explicit links, reminders, and action items. Queueing a
  draft creates a normal pending proposal; nothing executes until a user
  explicitly runs it.
- The review queue is powered by resurfacing candidates plus open-loop signals:
  processed/processing stage, high importance, next action, due reminders, stale
  action items, and older unreviewed notes. API responses include
  `review_reasons` and `review_priority`.
- Tags are normalized and alias-aware so manual tags and provider suggestions converge.
- Bookmark list retrieval filters by query, tag, domain, source, read state, and
  dates. Query matching includes bookmark text, annotations, and linked notes.
- Unified search has a durable per-user index for bookmarks and notes. The
  typed `/api/search/items` route returns ranked bookmark/note results with
  snippets and source links, and `/api/search/rebuild` exists as a
  quota-protected repair path for imports or operator recovery.
- The browser keeps bounded local read snapshots for high-use second-brain GET
  surfaces: saved pages, notes, Today inputs, Review, Inbox/work queues,
  reminders, memory jogger, and typed search. These snapshots make offline
  reading and recall useful without letting the service worker cache
  authenticated API responses.
- `/api/media/import` converts documents and transcript-style media into normal
  notes with `media:*` sources. EPUB, HTML, text, Markdown, pasted transcript,
  pasted OCR, best-effort PDF text, and provider-backed image OCR flow through
  the existing note inbox, export, review, and search index instead of creating a
  separate media silo.
- Cited answer mode uses the same unified retrieval layer and returns citations
  back to saved bookmarks or notes instead of producing uncited claims.
- Job status is visible per user, which makes background import/enrichment progress inspectable without exposing server errors.
- High-risk writes use lightweight SQLite mutation quotas before expensive work
  begins, including bookmark preview, import, saves, notes, inbox updates,
  links, reminders, and assistant proposal/approval.
- CLI-audience `/api/agent/*` routes expose scoped search, bookmark/note reads,
  note creation, action-item creation, reminder creation, and decision
  recording so an MCP wrapper can operate Arivu without web-session access.

## User Imports And Exports

- `/api/bookmarks/import` accepts safe URLs from common bookmark export shapes:
  JSON arrays, object-wrapped lists (`items`, `bookmarks`, `results`, or
  `links`), browser/Netscape HTML, OPML, RSS/Atom feeds, URL-bearing CSV/TSV
  exports such as Readwise or Kindle-derived tables, and newline URL lists.
- Imported bookmarks keep source hints such as browser, Pocket, Raindrop,
  Linkwarden, Linkding, and Karakeep/Hoarder when the export content identifies
  them.
- Import job reads expose total, fetched, AI-processed, failed, status, updated
  time, source report, and a bounded item sample so the UI can show progress
  without exposing server logs.
- `/api/bookmarks/export` supports JSON, CSV, browser HTML, Markdown bookmark
  links, and an Obsidian ZIP vault with bookmark/note files plus explicit graph
  links as wikilinks.
- Full JSON export and restore include inbox processing state, action items,
  notes, annotations, tags, searches, and review events remapped under the
  importing user.
- Full JSON export and restore also include knowledge objects, remapping
  bookmark, note, and object source references under the importing user.
- Explicit item links are also exported and restored with remapped bookmark and
  note IDs.
- Reminder rows are exported and restored with remapped item IDs, UTC due
  times, timezone, recurrence, notification channel, and completion state.
  Restores schedule only future email reminders to avoid flooding old backups.
- Full JSON backup/restore includes user-scoped `knowledge_feedback`. Old
  backups without this optional section remain valid. Derived graph edges and
  insight rows are not persisted as canonical records and rebuild from restored
  source data.

---

## Integrations & Direct-HTTP API Providers

To keep the dependency surface small, Arivu bypasses vendor SDKs. External communication runs through `/internal/providers/` over native standard library `net/http` calls wrapping typed JSON models.

### Model Providers (`gemini.go`, `model_provider.go`)
- **Use Case**: Performs automated summaries and insights through the configured text-generation provider. Gemini remains the image OCR and embedding provider for now, using `gemini-embedding-2` for semantic vectors.
- **Details**: Direct JSON endpoint payloads target Gemini-native generation, OpenAI-compatible chat completions, or Anthropic Messages based on `ai_provider`. Runtime settings read the active Model Provider, Model, API Key, and Base URL from SQLite/env, with legacy Gemini settings used only as Gemini fallbacks.

### Resend (`resend.go`)
- **Use Case**: Triggers transactional email verification notices.
- **Details**: Structured REST client communicating directly with Resend APIs.

### X Sync (`x.go`)
- **Use Case**: Syncs bookmarked, liked, or saved tweets directly into the user's permanent SQLite collection.
- **Details**: Employs direct API endpoints wrapping OAuth state connections to ingest recent histories.
- **Workers**: Managed securely in `/internal/app/x.go` by picking active synchronization tasks from the durable jobs queue and executing them in the background.
