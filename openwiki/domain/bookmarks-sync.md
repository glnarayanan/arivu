# Domain & Processing Workflows

Arivu is not just a bookmark vault; it is a knowledge engine that processes URL captures, discovers duplicate materials, associates semantic tags, and maintains live timelines.

---

## Semantic Knowledge Graph

Saving a bookmark initiates several processing stages to build a structured graph:

1. **Extraction**: The `safefetch` engine fetches safe page contents and sanitizes standard markup.
2. **Analysis**: Text content is scanned to extract main keyphrases (`entities` and `concepts`).
3. **Graph Relationships**:
   - Establishes linkages between bookmarks and distinct parsed keywords.
   - Computes intersection degrees to link related bookmarks.
   - Generates active data for the memory joggers and resurfacing prompts (e.g., reminding developers of neglected topics after logical durations).

---

## Duplicate Ingest Detection

To optimize storage and prevent workspace clutter, saving a bookmark triggers a duplicate analysis algorithm:
- Bookmarks are categorized and grouped based on canonicalized web path signatures and matching title hashes.
- Suggests consolidating items into unified groups when identical content spans multiple variations.

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
