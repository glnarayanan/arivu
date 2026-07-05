# Domain & Processing Workflows

Arivu is not just a bookmark vault; it is a knowledge engine that processes URL captures, discovers duplicate materials, associates semantic tags, and maintains live timelines.

---

## Semantic Knowledge Graph

Saving a bookmark initiates several processing stages to build a structured graph:

1. **Extraction**: The `safefetch` engine validates the URL, fetches safe page contents, and sanitizes standard markup.
2. **Analysis**: Text content is scanned locally to extract summary bullets, highlight candidates, suggested tags, entities, and concepts. Gemini embeddings are added only when a provider key is configured.
3. **Graph Relationships**:
   - Establishes linkages between bookmarks and distinct parsed keywords.
   - Computes intersection degrees to link related bookmarks.
   - Generates active data for the memory joggers and resurfacing prompts (e.g., reminding developers of neglected topics after logical durations).

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
- Cited answer mode uses the same unified retrieval layer and returns citations
  back to saved bookmarks or notes instead of producing uncited claims.
- Job status is visible per user, which makes background import/enrichment progress inspectable without exposing server errors.
- High-risk writes use lightweight SQLite mutation quotas before expensive work
  begins, including bookmark preview, import, saves, notes, inbox updates,
  links, reminders, and assistant proposal/approval.

## User Imports And Exports

- `/api/bookmarks/import` accepts safe URLs from common bookmark export shapes:
  JSON arrays, object-wrapped lists (`items`, `bookmarks`, `results`, or
  `links`), browser/Netscape HTML, OPML, RSS/Atom feeds, URL-bearing CSV/TSV
  exports such as Readwise or Kindle-derived tables, and newline URL lists.
- Imported bookmarks keep source hints such as browser, Pocket, Raindrop,
  Linkwarden, Linkding, and Karakeep/Hoarder when the export content identifies
  them.
- `/api/bookmarks/export` supports JSON, CSV, browser HTML, Markdown bookmark
  links, and an Obsidian ZIP vault with bookmark/note files plus explicit graph
  links as wikilinks.
- Full JSON export and restore include inbox processing state, action items,
  notes, annotations, tags, searches, and review events remapped under the
  importing user.
- Explicit item links are also exported and restored with remapped bookmark and
  note IDs.
- Reminder rows are exported and restored with remapped item IDs, UTC due
  times, timezone, recurrence, notification channel, and completion state.
  Restores schedule only future email reminders to avoid flooding old backups.

---

## Integrations & Direct-HTTP API Providers

To keep the dependency surface small, Arivu bypasses vendor SDKs. External communication runs through `/internal/providers/` over native standard library `net/http` calls wrapping typed JSON models.

### Gemini (`gemini.go`)
- **Use Case**: Performs automated summaries, insights, and embedding generation.
- **Details**: Direct JSON endpoint payload structure targeting Google Gemini endpoints, reading active access keys securely from the database settings envelope.

### Resend (`resend.go`)
- **Use Case**: Triggers transactional email verification notices.
- **Details**: Structured REST client communicating directly with Resend APIs.

### X Sync (`x.go`)
- **Use Case**: Syncs bookmarked, liked, or saved tweets directly into the user's permanent SQLite collection.
- **Details**: Employs direct API endpoints wrapping OAuth state connections to ingest recent histories.
- **Workers**: Managed securely in `/internal/app/x.go` by picking active synchronization tasks from the durable jobs queue and executing them in the background.
