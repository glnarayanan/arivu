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
- Bookmark detail renders summaries, tags, related items, read state, annotations, linked notes, and review completion in one place.
- The review queue is powered by resurfacing candidates and records completion or snooze events.
- Tags are normalized and alias-aware so manual tags and provider suggestions converge.
- Retrieval filters by query, tag, domain, source, read state, and dates. Query
  matching includes bookmark text, annotations, and linked notes.
- Cited answer mode summarizes the matching set and returns citations back to
  saved bookmarks instead of producing uncited claims.
- Job status is visible per user, which makes background import/enrichment progress inspectable without exposing server errors.

## User Imports And Exports

- `/api/bookmarks/import` accepts safe URLs from common bookmark export shapes:
  JSON arrays, object-wrapped lists (`items`, `bookmarks`, `results`, or
  `links`), browser/Netscape HTML, and newline URL lists.
- Imported bookmarks keep source hints such as browser, Pocket, Raindrop,
  Linkwarden, Linkding, and Karakeep/Hoarder when the export content identifies
  them.
- `/api/bookmarks/export` supports JSON, CSV, browser HTML, and
  Markdown/Obsidian-style links.

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
