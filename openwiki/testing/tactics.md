# Quality Assurance & Testing

Arivu prioritizes automated regression checks and rigorous validation.

---

## Testing Types

The repository defines split test targets spanning different logic surfaces:

### 1. Isolated Unit Tests
Testing for key behaviors, including:
- Host resolution, redirect counters, and range block logic inside `internal/safefetch/safefetch_test.go`.
- Article extraction fixtures inside `internal/safefetch/safefetch_test.go` should prove that chrome, JSON, script, and style data do not feed reading time or summaries.
- Restrictive CSS, HTML, and scripting scrubbing inside `internal/sanitize/sanitize_test.go`.
- Password decryption structures, session validations, and tokens inside `internal/auth/auth_test.go` or `internal/migrate/`.

### 2. Live HTTP Integration & Contract Tests
Implemented in `/internal/app/app_test.go`.
- Instantiates live local server contexts using `net/http/httptest`.
- Verifies exact HTTP middleware structures, payload limitation limits (e.g. attempting to submit overly bloated requests), cookie generation paths, and route rules.

### 3. Golden Output Tests
As shown in `/internal/app/golden_test.go`, Arivu uses stored mock datasets under `/internal/app/testdata/golden/` (such as `duplicate_groups.json`, `graph_summary.json`, `analytics_summary.json`).
These ensure updates to rendering mechanics do not introduce drift or semantic mapping regressions.

### 4. Knowledge-Surface Contracts

`internal/bookmarks/knowledge_surfaces_test.go` verifies cursor stability and
user isolation for Library, bounded focused Graph v2 payloads, owned Insight
evidence, derivative feedback filtering, optional old-backup compatibility, and
user-scoped knowledge-feedback export. Existing graph/analytics goldens remain
unchanged so additive v2 work cannot silently redefine legacy endpoints.

`internal/app/webtest/knowledge-shell.test.mjs` locks the four primary
destinations, query-preserving compatibility aliases, additive endpoint usage,
raw-JSON removal from normal object creation, global Capture/Search, the
light-only theme contract, and the accessible graph list.

---

## Browser Smoke Checks

Frontend smoke checks stay outside the checked-in dependency tree. Use a running
Go binary and temporary SQLite database, create a user, and seed enough content
for explicit and derived relationships plus at least one deterministic insight.

Cover these canonical routes:

- `/today`, including Pulse, Focus, Review, and Board contexts
- `/library`, including filters, cursor continuation, Capture, Inbox, objects,
  and duplicate maintenance
- `/search` in Search and Ask modes
- `/graph` in recent and focused modes plus the accessible node list
- `/insights`, evidence navigation, next actions, and all feedback controls
- `/bookmark/:id`, `/notes/:id`, `/settings`, and `/admin` for an admin user

Also load `/dashboard`, `/knowledge-graph`, `/analytics`, `/inbox`, `/focus`,
`/review`, `/board`, `/assistant`, `/objects`, `/evolution`, `/duplicates`, and
`/imports`; confirm the canonical URL and incoming query values survive.

Run at desktop, tablet, and 390x844 mobile sizes with the Brightlight-derived
light-only palette. Emulate an OS dark preference and verify the UI remains
light. Check keyboard-only use, visible focus, route announcements, skip link,
dialog/menu focus management, graph SVG/list parity, the Graph zoom/reset
controls, ordinary browser/pinch zoom, reduced motion, offline capture, cached
reads, loading/empty/failure/long-text states, no viewport overflow, WCAG AA
contrast, and an empty completed-flow console.

Annotation changes additionally need the reader selection composer checked for
pointer and keyboard selection, quote-only save, Save failure retry,
Escape/Cancel, source jump, and the unchanged manual disclosure fallback. At
390x844, the composer must anchor above the bottom safe area. The first-party
`extension/background.test.mjs` covers the external capture request, opt-in
registration, permission-off state, source-page context-menu behavior, and
expired-token cleanup; browser smoke validates the visible page surfaces.

---

## Checklist for Future AI Agents

When submitting modifications or adding enhancements to the Arivu codebase, you **MUST** ensure the following rules are strictly preserved:

1. **Clean Test Executions**:
   Ensure all tests execute cleanly without error:
   ```bash
   GOCACHE=/private/tmp/arivu-build-cache go test ./...
   node --check internal/app/web/app.js
   node --check internal/app/web/sw.js
   node --test internal/app/webtest/*.test.mjs
   node --check extension/background.js
   node --check extension/content.js
   node --check extension/popup.js
   node --check extension/selection-overlay.js
   node extension/url-utils.test.mjs
   node extension/content.test.mjs
   node extension/background.test.mjs
   ```
2. **Standard Library Over Custom Dependencies**:
   Do not introduce third-party HTTP routers, custom caching packages, or framework abstractions. Maintain Go `net/http` standard libraries.
3. **SSRF Guard Rail Verification**:
   If modifying network handlers, verify that outbound dialers *never* call unrestricted destinations using arbitrary redirects.
4. **HTML Sanitization Check**:
   Any rendering of third-party string constructs must be filtered by the backend sanitizer prior to ingestion and db save operations.
5. **Pragma Integrity**:
   Do not alter database transaction models or write modes without thorough evaluation of thread safeties. Check WAL patterns and close database files explicitly when processes end.
