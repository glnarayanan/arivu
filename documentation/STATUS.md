# Implementation Status

**Status:** Standalone repository initialized; parity implementation complete; release validation in progress.

## Implemented

- Go module and `arivu` command with `serve`, `migrate`, `version`, `login`, `save`, `list`, and `search` commands.
- `net/http` server with explicit timeouts, request body limits, panic recovery, request logging, and security headers.
- Embedded dependency-free frontend assets served from the Go binary.
- SQLite schema for users, sessions, bookmarks, summaries, collections, normalized relationships, import jobs, X connections, OAuth state, runtime settings, rate limits, audit events, and durable jobs.
- Opaque token auth with `web`, `cli`, and `extension` audiences, hashed token storage, refresh rotation, HTTP-only cookies, CSRF token checks, and bcrypt-to-Argon2id rehash on login.
- SSRF-aware safe fetch client with custom dialer, no proxy environment use, redirect validation, blocked private/reserved targets, content-type validation, and response-size limits.
- Backend-owned HTML sanitizer with strict allowlisted tags, attributes, and URL schemes.
- Direct HTTP provider clients for Gemini, Resend, and X API calls without provider SDK dependencies.
- Core bookmark, collection, search, analytics, admin, import/export, duplicate detection, semantic graph, resurfacing, memory jogger, and X sync behavior.
- Legacy JSON export migration validation and SQLite import executor with secret re-encryption, relationship checks, archived HTML sanitization, embedding validation, and intentional legacy session invalidation.
- Production packaging with Dockerfile, Compose sample, hardened systemd unit, and environment template.

## Verification

```bash
GOCACHE=/private/tmp/arivu-build-cache go test ./...
GOCACHE=/private/tmp/arivu-build-cache go build -trimpath -ldflags="-s -w" -o /private/tmp/arivu-check ./cmd/arivu
```

Current coverage includes schema initialization, sanitizer allowlist behavior, safe URL validation, import URL extraction, migration unknown-field rejection, direct HTTP handler integration tests, browser-facing first-run contracts, and golden parity fixtures.

## Follow-Up

- Run Docker image verification where Docker is available.
- Capture browser screenshots in CI if the project accepts a test-only browser dependency.
