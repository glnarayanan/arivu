# Quality Assurance & Testing

Arivu prioritizes automated regression checks and rigorous validation.

---

## Testing Types

The repository defines split test targets spanning different logic surfaces:

### 1. Isolated Unit Tests
Testing for key behaviors, including:
- Host resolution, redirect counters, and range block logic inside `internal/safefetch/safefetch_test.go`.
- Restrictive CSS, HTML, and scripting scrubbing inside `internal/sanitize/sanitize_test.go`.
- Password decryption structures, session validations, and tokens inside `internal/auth/auth_test.go` or `internal/migrate/`.

### 2. Live HTTP Integration & Contract Tests
Implemented in `/internal/app/app_test.go`.
- Instantiates live local server contexts using `net/http/httptest`.
- Verifies exact HTTP middleware structures, payload limitation limits (e.g. attempting to submit overly bloated requests), cookie generation paths, and route rules.

### 3. Golden Output Tests
As shown in `/internal/app/golden_test.go`, Arivu uses stored mock datasets under `/internal/app/testdata/golden/` (such as `duplicate_groups.json`, `graph_summary.json`, `analytics_summary.json`).
These ensure updates to rendering mechanics do not introduce drift or semantic mapping regressions.

---

## Browser Smoke Checks

Frontend smoke checks stay outside the checked-in dependency tree. Use the
running Go binary plus a temporary SQLite database, then cover the flows listed
in `../../documentation/frontend-runtime.md`.

---

## Checklist for Future AI Agents

When submitting modifications or adding enhancements to the Arivu codebase, you **MUST** ensure the following rules are strictly preserved:

1. **Clean Test Executions**:
   Ensure all tests execute cleanly without error:
   ```bash
   GOCACHE=/private/tmp/arivu-build-cache go test ./...
   ```
2. **Standard Library Over Custom Dependencies**:
   Do not introduce third-party HTTP routers, custom caching packages, or framework abstractions. Maintain Go `net/http` standard libraries.
3. **SSRF Guard Rail Verification**:
   If modifying network handlers, verify that outbound dialers *never* call unrestricted destinations using arbitrary redirects.
4. **HTML Sanitization Check**:
   Any rendering of third-party string constructs must be filtered by the backend sanitizer prior to ingestion and db save operations.
5. **Pragma Integrity**:
   Do not alter database transaction models or write modes without thorough evaluation of thread safeties. Check WAL patterns and close database files explicitly when processes end.
