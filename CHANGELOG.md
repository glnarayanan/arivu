# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- Created the standalone low-dependency Go repository for Arivu.
- Included the embedded frontend, SQLite persistence, auth/session subsystem, safe fetcher, sanitizer, job queue, provider clients, browser extension, deployment assets, and legacy migration tooling.
- Added contribution guidelines, code of conduct, GitHub sponsorship metadata, and expanded security reporting policy.
- Documented that `glnarayanan/arivu` is the active repository and `glnarayanan/arivu-legacy` is retained as the archived historical implementation.
- Added fork guidance for the canonical Go module path and made outbound fetch user-agent branding configurable.
- Added persistent frontend design context for future UI work.
- Added an interface quality audit covering accessibility, performance, theming, responsive behavior, and design anti-patterns.
- Added public password recovery and invite acceptance entrypoints to the embedded frontend.
- Added a tiny embedded SVG favicon for the browser UI.
- Added second-brain persistence for notes, bookmark-note links, annotations, normalized tags, tag aliases, saved searches, review events, and import source metadata.
- Added web APIs for notes, bookmark annotations, tags, tag aliases, saved searches, review completion/snoozing, and per-user background job status.
- Added cited local answer mode for saved-item search, with citations back to matching bookmarks.
- Added Markdown/Obsidian-style bookmark export and a real Settings import/export panel.
- Added Settings tag management for canonical tags and aliases.
- Added Settings profile and API key panels backed by the existing profile, password-change, and admin key routes.
- Added Settings connection controls for X status, connect, sync, and disconnect using the existing X routes.
- Added an admin audit log API and Admin page panel for recent sensitive account, settings, and auth events.
- Added a standalone Notes screen for creating, editing, and deleting freeform notes.
- Added deterministic bookmark enrichment for summaries, bullets, highlight quotes, suggested tags, graph entities, and graph concepts when provider AI is not configured.
- Added real embedded UI routes for review, daily memory, duplicate detection/merge, knowledge graph exploration, bookmark annotations, linked notes, read state, related items, and processing status.
- Added an installable PWA manifest with a mobile/browser share target that pre-fills dashboard capture.
- Added a reader control for copying selected archived text into a new quote annotation.
- Added inline reader controls for editing and deleting saved annotations.
- Added standalone notes to the daily review queue, with note completion and snooze support.
- Added a durable Inbox processing loop for bookmarks and notes, including per-item stage, priority, next action, reader controls, review context, and JSON backup/restore support.
- Added explicit bookmark/note links with backlink reads, bookmark-page controls, per-user ownership validation, and JSON backup/restore support.
- Added reminders for bookmarks and notes with due-time reads, completion/deletion APIs, bookmark-page controls, ownership validation, and JSON backup/restore support.
- Added an assistant action approval ledger for bounded second-brain mutations, with proposal, approve, reject, failed-action tracking, and an embedded Assistant review page.
- Added durable action items for bookmarks and notes, with inline Inbox/bookmark controls, completion/deletion APIs, ownership validation, and JSON backup/restore support.
- Added a Focus page that gathers pending action items and reminders without adding backend dependencies.
- Added note-side action item and reminder controls so standalone notes can carry the same open loops as bookmarks from the Notes page.
- Added note-side explicit links and backlinks, including note-to-note link creation from the Notes page.
- Added a unified per-user search index for bookmarks and notes, a typed `/api/search/items` retrieval API, and a quota-protected `/api/search/rebuild` repair path.
- Added timezone-aware recurring reminders with custom day intervals, notification channels, edit and snooze APIs, due-state response metadata, and idempotent scheduled Resend email jobs.
- Added an Assistant planning endpoint and guided UI that generate inert, reviewable drafts from Inbox, Review, search, or a specific saved item before queueing any proposal.
- Added a `/notes/:id` note workspace, note-to-bookmark links, Inbox bulk triage, keyboard Inbox stage shortcuts, Review reason labels, and Focus views for pending, overdue, today, upcoming, and completed open loops.
- Added a slim `/api/link-targets` route for bookmark/note link pickers so
  detail pages can populate relationship selectors without fetching note bodies
  or bookmark archive text.
- Added a `/today` signed-in cockpit with per-day daily notes and existing
  Inbox, Focus, Review, recent-note, and memory-jogger signals.
- Added a dependency-free command palette on the global Actions button and
  `Cmd/Ctrl+K` for navigation, capture, notes, search, cited answers, and
  current-item task/reminder/link actions.
- Added URL-bearing OPML, RSS/Atom, and CSV/TSV import parsing for feed lists
  and Readwise/Kindle-style exports that include source URLs.
- Added user-scoped recall feedback for search, cited answers, and Review,
  including why-shown metadata, freshness scores, backup/restore support, and
  never-resurface handling.
- Added an app-shell service worker and offline dashboard capture queue that
  replays saved URLs when the signed-in browser comes back online.
- Added browser-native dictation controls for daily notes, dashboard quick
  notes, standalone notes, and bookmark-linked notes.

### Changed

- Removed stale Mongo-named migration CLI options; migration now requires a
  dependency-free legacy JSON export via `--json-export`.
- Migration apply reports now include source document counts, skipped legacy
  sessions, and JSON-formatted errors on failed apply runs.
- Admin API key settings now take effect at runtime through SQLite overrides,
  with encrypted provider secrets, plain operational settings, source-aware UI
  status, and per-setting override removal.
- The Admin page now exposes overview, API usage, users, system, activity,
  collections, and audit sections backed by SQLite-native admin endpoints.
- Admin password reset now uses the same Argon2id password storage as user
  password reset and change-password flows.
- The embedded frontend admin reset flow now uses the existing accessible dialog
  system instead of a native browser prompt.
- The browser extension token bootstrap now sends the web session CSRF token
  when requesting extension-audience tokens.
- The embedded frontend visual system is quieter on dense app surfaces, with
  reduced decorative background patterning and no side-accent blockquote border.
- Consolidated public project documentation under `openwiki/` and removed the
  duplicate `documentation/` tree.
- Consolidated v2 provider-secret sealing/opening in `internal/secrets` and
  removed unused internal helpers and inert frontend state.
- Polished the embedded frontend with stronger interaction states, route loading feedback, semantic toasts, accessible search and navigation affordances, safer mobile layout behavior, and refined design tokens.
- Resolved the frontend audit findings with explicit route access metadata, inline form errors, assertive error toasts, and a quieter OKLCH-based visual system.
- Optimized embedded frontend asset delivery with content ETags, cache revalidation headers, zero-copy byte readers, and offscreen grid rendering containment.
- Refined the embedded second-brain workflow UI so Dashboard is capture-first
  with collapsible filters and saved searches, bookmark detail is reader-first
  with collapsed workbench groups, Inbox/Focus/Review use clearer user-facing
  labels, and the mobile shell keeps brutalist navigation compact.
- Added a user-facing OpenWiki guide and a GitHub Wiki publishing runbook so
  public wiki pages can be generated from repository documentation without
  making the GitHub Wiki the canonical source.
- Added a curated GitHub Actions wiki sync that republishes selected Wiki pages
  from `README.md` and `openwiki/` after documentation changes land on `main`.
- Bookmark saves now accept quick notes, selected quotes, and manual tags, and return a `job_id` so the UI can show enrichment progress.
- Extension selected-text saves now persist as quote annotations, with a backend compatibility alias for the older `annotation` payload field.
- Extension popup saves now accept quick notes and comma-separated tags.
- Extension popup saves now persist page titles and offer direct post-save links to the Inbox or saved bookmark instead of closing immediately.
- Extension self-hosted setup now requests browser host permission for the saved API origin and registers the token content script dynamically, avoiding manual manifest edits for custom domains.
- CI now checks embedded frontend and extension JavaScript syntax and runs the extension URL/origin self-test.
- Removed the extension popup's remote Google Fonts import in favor of native system font stacks.
- Bookmark list and search now support normalized tag, domain, source, read-status, and created-date filters, and text search includes linked annotations and notes.
- Cited answer mode now uses the unified retrieval layer and synthesizes deterministic answer text from saved summaries, highlights, snippets, linked context, and notes while preserving citations back to the source items.
- Bookmark import now accepts safe URLs from JSON arrays, object-wrapped exports, browser/Netscape HTML, and newline URL lists while recording source hints for inserted bookmarks.
- Imported bookmarks now create summary placeholders before processing and use safer duplicate counting for import reports.
- Imported bookmark processing now updates the visible import job counters for fetched, AI-processed, and failed items.
- Import jobs now include source reports and bounded item provenance so migration status can show where imported items came from.
- Imported bookmarks now persist their detected source for source filtering.
- JSON export now includes second-brain backup data: bookmark details, summaries, tags and aliases, annotations, linked and standalone notes, saved searches, review events, import jobs, and import provenance.
- Full JSON backups can now be restored through bookmark import, remapping IDs under the authenticated user while preserving summaries, tags and aliases, annotations, linked and standalone notes, saved searches, review events, and import provenance.
- Added an Obsidian ZIP export that writes bookmark and standalone note Markdown files into vault-ready folders without adding production dependencies.
- Direct `/notes/:id` URLs now route to the full note workspace instead of the dashboard fallback.
- The embedded frontend now restores focus and announces route changes after
  SPA navigation, labels dense dashboard filters for assistive technology,
  clarifies destructive/assistant/import copy, and uses slimmer link-target
  reads on bookmark and note detail pages.
- Assistant proposal UI now uses pending/execution language instead of approval
  ledger wording, and Settings tag copy now describes primary tags and aliases
  in user-facing terms.
- Trimmed dead bookmark helper code while preserving the compatibility
  `/api/bookmarks/aged` route and runtime clock override.

### Security

- Web, CLI, and extension tokens are audience-isolated.
- Login, forgot-password, and reset-password endpoints now use the existing SQLite `rate_limits` table to throttle repeated auth attempts.
- Sensitive admin/account mutations now write `audit_events` rows, and provider setting updates are restricted to known keys.
- CI now verifies module checksums, runs pinned `govulncheck`, and enables Dependabot for Go modules and GitHub Actions.
- CI now uploads a short-lived release evidence bundle with the Linux build, Go build info, module inventory, and SHA-256 checksums.
- Updated the Go baseline to 1.25 and upgraded `golang.org/x/net` to a release that fixes reachable HTML parser vulnerabilities in the sanitizer path.
- Server-side URL fetching pins connections to vetted IPs and blocks private/reserved targets.
- Archived HTML is sanitized by the backend before storage/display.
- GitHub Actions CI now declares least-privilege `contents: read` token permissions.
- Updated vulnerable Go modules to patched `golang.org/x/crypto` and `golang.org/x/net` releases.
- Derived new encrypted provider-secret keys with HKDF before AES-256-GCM sealing.
- Audit metadata is redacted before storage so token-, password-, secret-, and
  API-key-like values are not persisted in audit rows.
- Made web access and refresh cookie setters explicitly HttpOnly while keeping the CSRF double-submit cookie readable.
- Validated imported URLs with the same SSRF-aware safe-fetch policy used for normal saves.
- Full JSON backup restore writes every restored row with the authenticated user ID and remaps cross-references instead of trusting exported ownership.
- Escaped formula-like CSV export cells to reduce spreadsheet injection risk.
- Obsidian ZIP export sanitizes generated filenames and reuses existing Markdown escaping for file content.
- Obsidian ZIP export now emits bookmark/note graph links as vault-ready wikilinks and writes linked notes as note files.
- Covered new second-brain routes with CSRF, audience isolation, and cross-user isolation tests.
- Assistant actions are inert until explicit approval, revalidate item ownership during execution, and record failed stale proposals without running arbitrary tools.
- Authenticated mutation quotas now throttle high-risk write paths with hashed per-user, per-audience keys in SQLite.
- Search index rebuilds are explicit CSRF-protected mutations; read-only search routes do not mutate server state and typed search results remain user-scoped.
- Reminder update and snooze routes are CSRF-protected, quota-limited, and user-scoped; reminder email jobs re-check ownership, due timestamp, pending status, and `last_notified_at` before sending.
- Assistant suggestions are CSRF-protected, quota-limited, user-scoped, and ephemeral; queueing and approval continue to validate allowlisted proposal payloads before any mutation runs.
- Inbox bulk triage is CSRF-protected, quota-limited, user-scoped, and returns per-item failures for stale or cross-user targets.
- Bookmark cards now escape saved titles, domains, and descriptions before inserting them into the embedded frontend.
