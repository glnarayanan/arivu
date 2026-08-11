# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

- Upgraded the isolated capture runtime to Playwright 1.62.1 and JSDOM 30.0.1,
  removing the vulnerable transitive Undici 7 dependency; raised its documented
  Node.js minimum to 22.22.2. Also upgraded the bundled SQLite driver to 1.14.49
  and GitHub Actions' Go setup action to v7.

## [1.5.0] - 2026-07-15

- Fixed complete browser captures to fetch every image referenced by the
  Readability projection through the attempt-scoped safe proxy, before optional
  preservation artifacts consume the byte budget. Browser navigation now uses
  a normal Chromium identity so bot-sensitive publishers do not degrade into a
  stored `Forbidden` reader page.
- Added the same owner-scoped reader-image localization to ordinary direct HTTP
  captures, so installations without the optional browser helper still download,
  store, and rewrite article figures instead of silently removing them. Text-only
  captures now report `saved` rather than inaccurately claiming `preserved`.
- Corrected reading-time estimates to use 238 words per minute with upward
  rounding. When a model provider is unavailable or rejects its output,
  summaries now prefer the publisher's description and preserve decimal version
  numbers instead of truncating titles such as `Claude Code 2.0` to `2`. Rich
  model output is salvaged field-by-field after validation instead of losing all
  executive-summary paragraphs, key points, and highlights because one field
  failed; evidence-derived rich fields remain available during provider outages.
- Made Administration's API Usage view operationally useful by listing durable
  failed jobs and summaries with owners, bookmark links, attempts, timestamps,
  provider/model metadata, safe provider error codes, and recorded failure
  details. Admins can safely retry failed bookmark-processing jobs without
  deleting their failure history; duplicate active retries are rejected and
  every retry is audited. Failure tables now retain legible column widths and
  non-breaking actions, and admins can bulk retry selected failures or the most
  recent failed processing attempt per bookmark for a specific user.
- Preserved safe native MathML in archived reader content so matrices,
  fractions, superscripts, and display equations retain their mathematical
  layout instead of collapsing into a wall of text. Equally good successful
  recaptures now refresh older evidence, so Reprocess activates extractor fixes
  rather than retaining a stale reader solely because its quality score tied.
- Reduced Library noise by showing saved and user-authored items by default and
  moving generated concepts and entities into a clearly labeled secondary view;
  both remain searchable and available in Graph.
- Restored keyboard-first workflows from the legacy application: quick capture,
  search focus, quick actions, printing, saved-item navigation, and Escape
  handling. A discoverable `?` shortcut guide is also available from Quick
  actions, while single-key commands stay disabled during form entry and in
  dialogs. Persistent Capture, Search, and More actions show compact shortcut
  hints on larger screens so the commands can be learned without opening the
  guide.

## [1.4.0] - 2026-07-14

- Added the optional isolated browser-capture service using pinned Playwright /
  Chromium, Mozilla Readability, JSDOM, and Monolith dependencies. Rendered
  capture runs headlessly in the background through an attempt-scoped,
  authenticated, SSRF-safe egress proxy and cannot open a user-visible tab.
- Added evidence selection that can upgrade weak direct extraction from the
  rendered DOM without allowing a challenge page or failed browser run to
  replace the last good reader copy.
- Added owner-scoped, content-addressed reader media, local library thumbnails,
  responsive figures, screenshots, PDFs, source responses, and self-contained
  HTML preservation. Offline previews are lazy, sandboxed, server-inerted, and
  capped separately from downloadable artifacts.
- Added installer-managed native capture artifacts for AMD64 and ARM64 with
  streaming checksum verification, safe bounded extraction, dependency and
  preflight checks, hardened systemd activation, opt-out/reconfigure controls,
  bounded downloads, Ubuntu 22.04+/Debian 12+ native compatibility, and
  transactional upgrade rollback. Operators never need Docker or npm, while the
  Go binary and embedded frontend remain dependency-free.

## [1.3.0] - 2026-07-13

- Fixed Home so Pulse, Focus, Review, and Board keep their shared navigation;
  separated Focus status filters from Home routing; removed Pulse's cross-column
  whitespace; condensed Review with deduplicated memories and optional follow-up
  forms; and made Board fill wide desktops while retaining horizontally
  scrollable Kanban lanes on constrained screens.
- Added owner-scoped nested collections with ordered siblings, safe moves and
  cycle prevention, non-recursive deletion, and hierarchy-aware backup restore.
- Added an opt-in, dependency-free browser preservation helper boundary with
  strict JSON, timeout, staging confinement, format/MIME/size validation, and
  authenticated screenshot/PDF/HTML artifact presentation.
- Added capture-attempt and typed-artifact persistence, evidence-version binding
  for annotations, crash-safe bounded storage of original direct-fetch responses,
  owner-checked artifact APIs, and stable capture terminal states without changing
  the one-binary SSRF-safe direct-fetch baseline.
- Added Settings controls for per-user AI tagging and RSS/Atom subscription
  lifecycle/status, bookmark scroll-progress and capture-history presentation,
  retained-evidence inspection, collection management, and one-time-copy public
  bookmark links with owner list/revoke controls. Removed the duplicate profile
  import/export action and normalized legible hover states across clickable UI.
- Added snapshot public shares with digest-only tokens, expiry/revocation,
  dedicated non-leaking projections, bounded public RSS, explicitly selected
  screenshot/PDF artifacts, rate limiting, and noindex/no-store defaults.
  Membership now freezes selected evidence and all public fields, migrations
  safely backfill existing rows, public snapshots survive private bookmark
  deletion, and the reader uses self-hosted CSP-safe assets and product styling.
- Added CLI-audience agent parity for collections, feed subscriptions, and
  public-share lifecycle operations with bounded mutation quotas.
- Backups now cover adjacent assets with a verified size/hash manifest while
  retaining restore compatibility with legacy manifest-free backups.
- Artifact storage now supports multiple logical references per content object,
  enforces a configurable logical per-user quota, and safely migrates legacy
  `storage_key` uniqueness. Backup hashing streams data and excludes staging;
  restore validates live DB references and activates database/assets as a pair.
- Asset maintenance now reports missing referenced objects, retains shared live
  objects, and grace-cleans stale staging and unreferenced content safely.

## [1.2.1] - 2026-07-13

### Fixed

- Made database migration apply schema in additive-safe phases: create tables and
  unique indexes first, run column ensures, then create non-unique indexes. This
  lets existing installs gain quality metadata columns before indexes that
  reference them, so self-hosted upgrades no longer fail on start with
  `no such column: enrichment_version`.
- Install and upgrade paths now `chmod 0755` release binaries after write so a
  restrictive root umask cannot strip execute bits (`Permission denied` /
  systemd `203/EXEC`). Failed upgrades include best-effort `systemctl status`
  and recent `journalctl` output to speed diagnosis.

## [1.2.0] - 2026-07-12

### Added

- Added privacy-safe aggregate quality auditing and an explicit, dry-run-first
  reprocessing CLI with scope guards, verified backup matching, bounded durable
  batches, idempotent run tracking, and redacted run-status reporting.
- Added a 30-case synthetic quality corpus covering evidence, summary
  faithfulness, semantic precision, extraction failures, unusual text, and
  prompt injection without production bookmark contents.

### Changed

- Documented evidence provenance, bounded summary and semantic guarantees,
  insight-versus-recommendation semantics, and staged backup/repair/rollback.
- Kept incomplete and metadata-only captures as repairable bookmark records
  while excluding them from Graph unless the user intentionally connected them;
  focused Graph reads and relationship validation use the same eligibility rule.
- Hid unresolved X placeholders whose Library title and body are only the same
  `t.co` short link while retaining the bookmark record for later repair.
- Replaced generic `Post / X` projection labels with useful source context and
  made visual Graph nodes link directly to their saved knowledge destination;
  the accessible node list now uses the same direct links with a legible
  high-contrast hover state.
- Bounded provider work within a four-minute bookmark lease, made summary
  retries share one deadline, and replaced raw provider telemetry errors with
  safe classified codes and messages.
- Quarantined unversioned or unsupported legacy entities and concepts from
  Library, Graph, and Insights; source-aware reprocessing now reports missing X
  evidence as partial instead of completed, and X Sync repairs existing rotten
  duplicates from HTML-entity-decoded API evidence before requeueing analysis.
  Unvalidated legacy summaries no longer block a safe fallback swap when
  replacement model output fails validation, while unavailable historical X
  items keep their bookmark and receive only deterministic title decoding plus
  generated-state cleanup.

## [1.1.0] - 2026-07-11

### Added

- Promoted Notes to the primary desktop and mobile navigation and removed its
  duplicate profile-menu entry so the core writing workspace is always visible.
- Added the knowledge-centered Home, Library, Notes, Graph, Insights, and Search / Ask
  experience around the Capture -> Connect -> Discover -> Learn loop, while
  preserving legacy deep links as query-aware compatibility routes.
- Added cursor-based `/api/library/items`, bounded typed
  `/api/knowledge-graph/v2`, and evidence-backed `/api/insights` endpoints with
  strict user scoping, deterministic ordering, focused graph expansion,
  provenance, confidence, and durable relationship/insight feedback.
- Reworked the complete embedded interface with a Brightlight-derived,
  light-only warm editorial system: white and sand surfaces, coral accents,
  serif headings, sans-serif UI text, dashed rules, compact radii, pill
  controls, and restrained shadows. This changes look and feel only; routes,
  navigation, menus, options, content hierarchy, and functionality remain
  unchanged. OFL-licensed Geist and Noto Serif fonts are served first-party;
  the Astro/Tailwind reference and its licensed assets are not shipped.
- Added `knowledge_feedback` backup/restore support so useful, not-useful,
  snoozed, dismissed, and confirmed derivative feedback survives export while
  inferred relationships remain rebuildable.
- Added per-bookmark reprocessing from the reader while preserving manual data
  and the previous archive until refreshed content is ready.
- Added inline reader annotation capture with optional notes, selection-derived
  text-quote selectors, retryable errors, Escape/Cancel behavior, and a
  narrow-screen bottom composer while preserving the manual annotation form.
- Added opt-in browser-extension inline annotations for external page text,
  including dynamic optional-host registration, Arivu-origin exclusion,
  exact-URL bookmark get-or-create, and extension-audience capture tests.
- Added `arivu version`, `arivu --version`, `arivu-installer version`, and
  `arivu-installer --version`; tagged release builds inject the exact Git tag
  into both binaries so installed versions remain authoritative.
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
- Added model-provider settings for text generation with OpenAI-compatible, Anthropic, Gemini, local, and custom presets; non-admin users no longer see the API Keys settings tab, keyless local providers work without dummy secrets, and provider changes replace stale model and credential state atomically.
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
- Improved import trust signals with clearer supported-format copy and native
  progress bars over existing import job counters.
- Hardened article extraction for Substack-style newsletter markup, excluded
  discussion sections, retained page descriptions, and surfaced partial
  extraction instead of silently treating comment-only content as complete.
- Added Settings and API media import for EPUB/PDF/text/HTML/Markdown files,
  pasted transcripts, pasted OCR text, and optional Gemini-backed image OCR,
  saving each import as a searchable `media:*` note.
- Added browser-local read snapshots for saved pages, notes, Today, Review,
  Inbox/work queues, reminders, memory jogger, and typed search so recent
  second-brain views remain readable when offline.
- Added source-jump controls and text-quote selector metadata for reader
  annotations captured from archived page selections.
- Added typed knowledge objects, topic evolution, a fixed Today board, ICS
  meeting import, and CLI-audience agent routes for scoped search, reads, notes,
  tasks, reminders, and decision recording.
- Added JSON backup/restore coverage for knowledge objects with source-reference
  remapping.
- Added a first-party `arivu-installer` CLI for end-to-end Linux VPS installs,
  including shared-host preflight, proxy-mode planning, systemd/env rendering,
  backup/restore helpers, checksum verification, and operational commands.
- Added a checksum-verifying `deploy/install.sh` bootstrap script for the
  one-command installer flow.
- Added installer support for pinned release versions, TLS email rendering in
  Arivu-managed Caddy snippets, and reconfiguration defaults loaded from the
  existing generated env file.
- Added `arivu admin bootstrap --password-stdin` for installer-safe first-admin
  creation without passing passwords through shell arguments.
- Added a tag-based release workflow that publishes Linux app and installer
  artifacts, checksums, build info, module inventory, and provenance
  attestations.

### Changed
- Bookmark enrichment now preserves successful model-generated bullets,
  highlights, and suggested tags instead of overwriting them with local fallback
  text; bookmark pages render the associated long-form explanation, leading
  capture banner text is trimmed before analysis, and Gemini semantic search
  uses `gemini-embedding-2` after `text-embedding-004` retirement.
- Documented that local `go run` processes are fixed build snapshots: restart
  them after source or branch changes, while SQLite-backed runtime settings take
  effect without a restart; added the persistent local development health check.
- Installer upgrades now download and checksum-verify both the Arivu app and
  installer from the same release, replace them transactionally, and roll both back
  when service activation or health checks fail. The one-line bootstrap now
  detects an existing install and repairs older installers through an in-place
  upgrade instead of relaunching the setup wizard.
- Mutable embedded frontend assets now require cache revalidation. Service
  worker update checks bypass stale HTTP cache entries, online shell requests
  revalidate, and the offline shell cache advances to v3 so a successful binary
  upgrade cannot leave the previous interface active for hours.
- Import jobs now recover expired leases after worker crashes and count each
  bookmark's terminal import outcome once, so retryable failures no longer
  inflate progress totals. Job completion and failure updates are now fenced to
  the active lease so stale workers cannot overwrite a re-leased job.
- Full JSON backups now preserve X bookmark metadata, including tweet identity,
  author fields, tweet URL, and available metrics, and X restore replay dedupes
  by tweet identity as well as URL.
- Installer reconfigure keeps a disabled backup policy when the operator accepts
  defaults, actively disables an existing backup timer when backups are turned
  off, and restore checks local health before restarting backup timers or
  reporting success.
- Managed-Caddy installer output now prints manual firewall commands instead of
  claiming public HTTPS completion when UFW/firewalld still needs operator
  action, and app-only plans print Caddy, Nginx, and Apache snippets.
- Installer DNS mismatches are now warnings with Cloudflare/origin and
  reconfigure guidance instead of hard install blockers.
- Installer plan confirmation now reads from the terminal during `curl | sudo
  bash` installs instead of accepting piped EOF as the default cancellation.
- CI now runs the browser extension content-script test alongside the extension
  URL/origin test.
- Hardened the self-hosting installer with strict domain validation, Caddy vhost
  detection, HTTPS-only release downloads, checksum verification, service user
  bootstrap ownership, managed-Caddy activation, consistent SQLite backups,
  restore downtime safety, upgrade rollback, and reconfigure state preservation.
- Removed the duplicate OpenWiki legacy migration overview and kept the fuller
  migration guide as the canonical migration documentation.
- Reframed the README and generated GitHub Wiki home around Arivu's
  second-brain workflow instead of leading with implementation history.
- Removed stale Mongo-named migration CLI options; migration now requires a
  dependency-free legacy JSON export via `--json-export`.
- Migration apply reports now include source document counts, skipped legacy
  sessions, and JSON-formatted errors on failed apply runs.
- Admin API key settings now take effect at runtime through SQLite overrides,
  with encrypted provider secrets, plain operational settings, source-aware UI
  status, and per-setting override removal.
- Runtime app settings now cover public URL, signup policy, and secure-cookie
  behavior through the same SQLite override path used by provider settings.
- The Admin page now includes a Settings tab for public URL, signup policy,
  secure-cookie state, model-provider, Resend, and X configuration.
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

- Hardened scheduled OpenWiki updates by disabling unauthenticated LangSmith
  tracing, selecting OpenRouter explicitly, pinning the generator and
  pull-request action, and preventing generated output from rewriting its own
  privileged workflow or policy documentation while still capturing ordinary
  `openwiki/` and `AGENTS.md` edits.
- Library pagination now HTML-escapes user-controlled filter parameters before
  inserting the next-page URL into the embedded frontend, preventing DOM XSS.
- Web CSRF checks now bind the submitted cookie/header token to the authenticated
  session's stored CSRF hash.
- Runtime X OAuth redirect URI settings are validated for both Admin overrides
  and env/config fallbacks before they can be used in OAuth URLs.
- Safe fetch SSRF filtering now rejects additional IPv4 and IPv6 special-use
  transition, benchmark, documentation, shared, reserved, and non-public ranges.
- Runtime X OAuth redirect URIs now reject relative, malformed, non-HTTP(S), and
  whitespace/control-containing values.
- Server-side URL fetching now rejects shared, documentation, benchmark,
  multicast, reserved, IPv4-mapped, and IPv6 special-use targets in addition to
  private, loopback, link-local, and unspecified addresses.
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

## [1.0.1] - 2026-07-10

### Changed

- Restored legacy parity for key Go-port surfaces: X OAuth now completes from
  the Settings redirect callback, Analytics returns the combined legacy summary
  envelope with immediate first paint and resilient page rendering, bookmark
  extraction uses readable article text instead of raw DOM text, Gemini
  summaries populate structured summary fields with a local fallback status
  when AI is unavailable, Admin settings expose Gemini model/base URL controls,
  and the embedded UI returns to the sharp signal-orange/electric-blue brutalist
  theme.
- Consolidated repeated empty states, runtime-setting rows, and shared visual
  dimensions; moved service-worker registration into the CSP-approved app
  module, added lifecycle regression coverage, refreshed the offline shell
  cache, and contained mobile navigation within narrow viewports.
- Reworked the embedded frontend palette into accessible semantic OKLCH roles,
  then reduced supporting borders, shadows, typography weight, and motion while
  preserving primary brutalist work surfaces.
- Clarified core navigation, workflow counts, Assistant safeguards,
  destructive actions, provider settings, and admin controls with specific,
  outcome-focused interface copy.
- Bookmark cards now describe missing source metadata neutrally instead of
  implying that every blank description is waiting for enrichment; reader and
  import views remain the source of truth for processing status.
