# Implementation Status

**Status:** Standalone repository initialized; parity implementation plus capture-to-recall second-brain v1 are in progress.

## Implemented

- Go module and `arivu` command with `serve`, `migrate`, `version`, `login`, `save`, `list`, and `search` commands.
- `net/http` server with explicit timeouts, request body limits, panic recovery, request logging, and security headers.
- Embedded dependency-free frontend assets served from the Go binary.
- SQLite schema for users, sessions, bookmarks, `ai_summaries`, collections, normalized relationships, notes, annotations, normalized tags and aliases, saved searches, review events, import source metadata, import jobs, X connections, OAuth states, runtime settings, rate limits, audit events, and jobs.
- Opaque token auth with `web`, `cli`, and `extension` audiences, hashed token storage, refresh rotation, HTTP-only cookies, CSRF token checks, and bcrypt-to-Argon2id rehash on login.
- SSRF-aware safe fetch client with custom dialer, no proxy environment use, redirect validation, blocked private/reserved targets, content-type validation, and response-size limits.
- Backend-owned HTML sanitizer with strict allowlisted tags, attributes, and URL schemes.
- Direct HTTP provider clients for Gemini, Resend, and X API calls without provider SDK dependencies.
- Core bookmark, collection, search, analytics, admin, import/export, duplicate detection, semantic graph, resurfacing, memory jogger, and X sync behavior.
- Import accepts safe URLs from JSON arrays, object-wrapped exports, browser/Netscape HTML, and newline URL lists; imported bookmarks keep their detected source; queued import processing updates fetched, AI-processed, failed, completed status counters, aggregate source reports, and bounded item provenance on import detail; export supports JSON, CSV, browser HTML, and Markdown/Obsidian-style links.
- Second-brain APIs for notes, bookmark annotations, normalized tags, tag aliases, saved searches, review queue actions, and per-user job status.
- New bookmark saves accept quick notes, selected quotes, and manual tags, return the processing job ID, and populate deterministic enrichment fields even without provider keys.
- Extension selected-text saves create quote annotations through the extension audience route.
- The embedded UI now exposes graph, duplicates, daily memory, review queue actions for bookmarks and standalone notes, summaries, related items, tags, tag alias management, annotations, standalone and linked notes, saved searches, cited answer mode with standalone note citations, read state, review completion, analytics signals, and visible processing status.
- The embedded frontend includes a PWA manifest with a GET share target that pre-fills dashboard capture from shared title, text, and URL parameters.
- Legacy JSON export migration validation and SQLite import executor with secret re-encryption, relationship checks, archived HTML sanitization, embedding validation, and intentional legacy session invalidation.
- Production packaging with Dockerfile, Compose sample, hardened systemd unit, and environment template.

## Verification

```bash
GOCACHE=/private/tmp/arivu-build-cache go test ./...
GOCACHE=/private/tmp/arivu-build-cache go build -trimpath -ldflags="-s -w" -o /private/tmp/arivu-check ./cmd/arivu
```

Current coverage includes schema initialization, sanitizer allowlist behavior, safe URL validation, import URL extraction, import source detection, imported bookmark source persistence, import job payload/progress/source-report/provenance accounting, Markdown export escaping, migration unknown-field rejection, direct HTTP handler integration tests, second-brain route scoping and CSRF checks, tag/date filtering, cited answer mode with bookmark and standalone note citations, bookmark and standalone note review queues, browser-facing first-run and PWA manifest contracts, and golden parity fixtures.

Latest manual localhost smoke used a temporary SQLite database and verified:

- SPA route fallback and `/manifest.webmanifest`.
- Signup, cookie auth, and CSRF-protected bookmark import.
- Imported bookmark search/list results.
- Cited answer mode.
- Markdown export download.

## Follow-Up

- Run Docker image verification where Docker is available.
- Capture browser screenshots in CI if the project accepts a test-only browser dependency.
- Add browser smoke automation for annotation, review, duplicate merge, import/export, and mobile share flows.
- Defer PDF/OCR/native mobile until the web and extension capture-to-recall loop has usage proof.
