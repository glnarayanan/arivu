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

### Changed

- Polished the embedded frontend with stronger interaction states, route loading feedback, semantic toasts, accessible search and navigation affordances, safer mobile layout behavior, and refined design tokens.
- Resolved the frontend audit findings with explicit route access metadata, inline form errors, assertive error toasts, and a quieter OKLCH-based visual system.

### Security

- Web, CLI, and extension tokens are audience-isolated.
- Server-side URL fetching pins connections to vetted IPs and blocks private/reserved targets.
- Archived HTML is sanitized by the backend before storage/display.
- GitHub Actions CI now declares least-privilege `contents: read` token permissions.
- Updated vulnerable Go modules to patched `golang.org/x/crypto` and `golang.org/x/net` releases.
- Derived new encrypted provider-secret keys with HKDF before AES-256-GCM sealing.
- Made web access and refresh cookie setters explicitly HttpOnly while keeping the CSRF double-submit cookie readable.
