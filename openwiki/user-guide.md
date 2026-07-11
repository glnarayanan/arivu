# User Guide

Arivu is a self-hosted second brain for turning captured material into connected
knowledge and evidence-backed learning patterns.

The browser app uses a Brightlight-derived warm editorial look with a
white/sand canvas and coral accents. It is intentionally light-only. This is a
visual treatment only: the routes, navigation destinations, menus, options,
and workflows documented below are unchanged.

## Start Arivu

```bash
go run ./cmd/arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

Open `http://127.0.0.1:8080/auth`, create an account, and start from Home at
`/today`. Production deployments should use TLS and configure `COOKIE_SECURE`,
`SECRET_KEY`, `APP_URL`, `ADMIN_EMAILS`, and signup policy.

## Capture -> Connect -> Discover -> Learn

### Capture

Use the persistent Capture button from any authenticated screen. Choose Link,
Note, Quote, or File and save immediately. Organization and provider setup are
optional. Link and quote captures queue locally if the browser is offline;
notes and file imports require the server.

You can also capture from the browser extension, the PWA share target, or
`arivu save`. Shared PWA URLs still enter through `/dashboard`; Arivu preserves
the shared title, text, and URL while opening the canonical Library capture
context.

### Connect

Open a bookmark or note to add explicit links and inspect backlinks. Explicit
links are your durable knowledge. Graph may also show source, concept, entity,
and semantic-similarity relationships. Derived edges show provenance and
confidence; eligible suggestions can be confirmed or dismissed.

### Discover

Library (`/library`) contains saved pages, notes, daily notes, annotations,
knowledge objects, entities, and concepts. Filter by type, topic, source, stage,
date, or connection state. New captures need no folder or tag.

Use Search / Ask (`/search`) to retrieve matching knowledge or ask a cited
question. Cited answers use only material in your Arivu account and link back to
the evidence.

Graph (`/graph`) opens a bounded recent view instead of an unreadable global
network. Choose a focus node to expand its local neighborhood. Selecting a node
opens the inspector without leaving the graph. The open Accessible node list
contains the same nodes for keyboard and screen-reader use. The canvas remains
pannable when zoomed, offers explicit Zoom out, Reset view, and Zoom in
controls, and keeps normal browser/pinch zoom enabled.

### Learn

Insights (`/insights`) surfaces locally detected emerging themes, recurring
connections, forgotten value, knowledge gaps, and serendipitous connections.
Each card shows its explanation, time window, confidence, detection reason,
supporting sources, and useful next actions.

Use Useful, Not useful, Snooze, or Dismiss to shape what returns. Feedback is
private, included in full backup/restore, and never changes another user's
results.

## Home And Supporting Workflows

Home is a restrained knowledge pulse: daily note, new material, active tasks or
reminders, review candidates, recent notes, and one useful memory. Focus,
Review, and Board remain contextual Home views. Inbox remains a Library view for
stage, priority, next-action, bulk, and keyboard triage.

Tasks and reminders remain secondary attributes of bookmarks and notes. Review
continues to explain why an item returned and supports complete/snooze actions.
The More button and `Cmd/Ctrl+K` retain the command palette for navigation,
capture, search, cited Ask, and current-item actions.

## Reading, Notes, And Objects

Notes is a primary navigation destination alongside Home, Library, Graph, and
Insights, so the writing workspace is available without opening a nested menu.

Bookmark pages keep sanitized archived content, summaries, provenance, tags,
annotations, notes, links/backlinks, related items, tasks, reminders, and review
actions. Selecting archived text opens the inline annotation composer; the
manual disclosure form remains available.

`/notes/:id` is the full note workspace. Object creation now presents
approachable fields for projects, people, books, meetings, decisions, and
research threads. Normal object creation no longer asks for raw JSON.

## Browser Extension

The extension popup captures pages with title, note, tags, and collections.
Context-menu and keyboard saves keep their existing behavior. Optional inline
annotations request page permission only after the user enables them, exclude
the Arivu origin, and save selected text explicitly. The server reuses an exact
URL bookmark or creates one and queues normal enrichment.

## Offline And Provider-Optional Use

Arivu keeps bounded browser snapshots for supported recent reads. If the network
drops, those surfaces may show the latest local copy. Auth, writes other than
queued link/quote capture, and admin data remain network-only.

A model provider is optional. Without one, Arivu still captures, extracts,
sanitizes, searches, links, reviews, imports, exports, builds the local graph,
and generates deterministic insights. Missing embeddings only remove semantic
similarity relationships; explicit and other local relationships remain.

## Import, Export, Backup, And Migration

Settings imports browser, Pocket, Raindrop, Linkwarden, OPML, RSS/Atom,
URL-bearing Readwise/Kindle CSV or TSV, Arivu JSON, or one URL per line. Document
and transcript import accepts EPUB, PDF, text, Markdown, HTML, images with OCR
text, and pasted video transcripts. Calendar ICS creates meeting objects.

Exports include full JSON, CSV, browser HTML, Markdown, and Obsidian ZIP. Full
JSON preserves bookmarks, summaries, notes, daily notes, objects, explicit
links, annotations, tasks, reminders, review state, result feedback, knowledge
feedback, import provenance, and X metadata. Restore remaps owned IDs under the
importing account. Derived graph relationships and insights are rebuilt from
canonical data; their durable feedback is restored separately.

Legacy migrations use the JSON export process documented in
`domain/migration-guide.md`.

## Administration And Integrations

Profile, imports/exports, settings, and administration are under the profile
menu. Non-admin users do not see provider-secret controls. Admin provider fields
remain generic: Model Provider, Model, API Key, and Base URL. Local/keyless
providers remain supported.

CLI and agent audience routes keep scoped bookmark capture/search, saved-item
reads, notes, tasks, reminders, and decisions. Web, CLI, and extension tokens
cannot cross audience boundaries.

## Compatibility URLs

Saved links continue to work: `/dashboard`, `/knowledge-graph`, `/analytics`,
`/inbox`, `/focus`, `/review`, `/board`, `/assistant`, `/objects`, `/evolution`,
`/duplicates`, and `/imports` map into the corresponding canonical destination
without dropping query state.
