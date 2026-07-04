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
- Added deterministic bookmark enrichment for summaries, bullets, highlight quotes, suggested tags, graph entities, and graph concepts when provider AI is not configured.
- Added real embedded UI routes for review, duplicate detection/merge, knowledge graph exploration, bookmark annotations, linked notes, read state, related items, and processing status.
- Added an installable PWA manifest with a mobile/browser share target that pre-fills dashboard capture.

### Changed

- Removed the inert `arivu migrate --mongo-uri` discovery path; migration now
  requires a dependency-free JSON export via `--json-export` or
  `--mongo-export`.
- Consolidated v2 provider-secret sealing/opening in `internal/secrets` and
  removed unused internal helpers and inert frontend state.
- Polished the embedded frontend with stronger interaction states, route loading feedback, semantic toasts, accessible search and navigation affordances, safer mobile layout behavior, and refined design tokens.
- Resolved the frontend audit findings with explicit route access metadata, inline form errors, assertive error toasts, and a quieter OKLCH-based visual system.
- Optimized embedded frontend asset delivery with content ETags, cache revalidation headers, zero-copy byte readers, and offscreen grid rendering containment.
- Bookmark saves now accept quick notes, selected quotes, and manual tags, and return a `job_id` so the UI can show enrichment progress.
- Imported bookmarks now create summary placeholders before processing and use safer duplicate counting for import reports.

### Security

- Web, CLI, and extension tokens are audience-isolated.
- Server-side URL fetching pins connections to vetted IPs and blocks private/reserved targets.
- Archived HTML is sanitized by the backend before storage/display.
- GitHub Actions CI now declares least-privilege `contents: read` token permissions.
- Updated vulnerable Go modules to patched `golang.org/x/crypto` and `golang.org/x/net` releases.
- Derived new encrypted provider-secret keys with HKDF before AES-256-GCM sealing.
- Made web access and refresh cookie setters explicitly HttpOnly while keeping the CSRF double-submit cookie readable.
- Validated imported URLs with the same SSRF-aware safe-fetch policy used for normal saves.
- Escaped formula-like CSV export cells to reduce spreadsheet injection risk.
- Covered new second-brain routes with CSRF, audience isolation, and cross-user isolation tests.
