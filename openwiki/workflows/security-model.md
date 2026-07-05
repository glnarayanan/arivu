# Go Rewrite Security Model

## Auth

- Web, CLI, and extension clients receive opaque tokens with explicit audiences.
- Session tokens are stored only as one-way SHA-256 hashes in SQLite; raw bearer tokens are returned once and compared by hash on later requests.
- Web sessions use HTTP-only access/refresh cookies.
- Cookie-authenticated mutations require an `X-CSRF-Token` header matching the CSRF cookie.
- Password reset and password change revoke affected sessions.
- Login, forgot-password, and reset-password endpoints use SQLite-backed
  throttles with hashed rate-limit keys.
- Legacy bcrypt hashes are accepted and upgraded to Argon2id on successful login.
- Runtime provider secrets are encrypted with AES-256-GCM using HKDF-derived key material from `SECRET_KEY`; non-secret runtime settings stay in plain settings rows.
- Provider setting updates are restricted to known provider/runtime keys, and
  audit metadata records changed key names without storing secret values.
- Successful admin user mutations, provider setting updates, password changes,
  and password resets write `audit_events` rows.
- Admins can inspect recent audit events through a bounded newest-first
  `/api/admin/audit-events` route and the Admin page. The route is admin-only
  and returns changed provider setting names rather than secret values.
- Second-brain routes use the same web-audience boundary as bookmarks. Notes,
  annotations, tags, saved searches, review actions, and job status are all
  scoped by `user_id`.
- Collection membership writes verify both the collection and bookmark belong
  to the authenticated user before inserting relationship rows.
- Extension and CLI tokens cannot call web-audience second-brain routes.

## Fetching And Archived Content

- Server-side fetches use a custom HTTP transport with no proxy environment use.
- The dialer resolves and blocks private, loopback, link-local, multicast, and unspecified IP targets.
- Redirects are revalidated and capped.
- Response size and content type are capped before archived content is processed.
- Archived HTML is sanitized server-side with strict tag, attribute, and URL allowlists.
- CodeQL may classify the final outbound fetch as user-controlled because Arivu intentionally fetches user-submitted bookmark URLs; the mitigation boundary is `internal/safefetch` URL validation plus per-dial DNS/IP validation.

## Legacy Migration Cryptography

- Legacy exports used Python Fernet with a key derived as `base64url(SHA-256(SECRET_KEY))`.
- The migration importer reproduces that derivation only to decrypt existing legacy runtime secrets and X tokens, then immediately re-encrypts secrets with the new HKDF/AES-256-GCM format. Plain runtime settings stay plain.

## Browser Controls

- CSP is delivered as an HTTP response header.
- The frontend avoids third-party scripts, external fonts, and npm packages.
- Provider secrets never enter browser-delivered code.
- User-authored note, tag, annotation, and saved-search strings are escaped by
  the frontend before DOM insertion. Archived page HTML is the only direct HTML
  render path and remains backend-sanitized.

## Import And Export

- Import URLs are validated through `internal/safefetch` before persistence or
  background fetch scheduling.
- Import source hints are recorded as per-user bookmark metadata only after the
  bookmark insert succeeds.
- CSV export cells are trimmed and formula-prefixed values are neutralized so
  spreadsheet imports do not execute user-controlled formulas.
- Markdown export escapes bracket text and closing parentheses in URLs.
- Obsidian ZIP export uses Go's standard `archive/zip`, sanitizes generated file
  names, and emits only user-owned Markdown content from the authenticated
  export route.

## Operational Limits

- The HTTP server has explicit read, write, idle, and header timeouts.
- Request bodies are globally bounded.
- Background work is leased through SQLite jobs with retry state.
