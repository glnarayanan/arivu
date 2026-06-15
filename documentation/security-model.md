# Go Rewrite Security Model

## Auth

- Web, CLI, and extension clients receive opaque tokens with explicit audiences.
- Session tokens are stored only as one-way SHA-256 hashes in SQLite; raw bearer tokens are returned once and compared by hash on later requests.
- Web sessions use HTTP-only access/refresh cookies.
- Cookie-authenticated mutations require an `X-CSRF-Token` header matching the CSRF cookie.
- Password reset and password change revoke affected sessions.
- Legacy bcrypt hashes are accepted and upgraded to Argon2id on successful login.
- Runtime provider secrets are encrypted with AES-256-GCM using HKDF-derived key material from `SECRET_KEY`.

## Fetching And Archived Content

- Server-side fetches use a custom HTTP transport with no proxy environment use.
- The dialer resolves and blocks private, loopback, link-local, multicast, and unspecified IP targets.
- Redirects are revalidated and capped.
- Response size and content type are capped before archived content is processed.
- Archived HTML is sanitized server-side with strict tag, attribute, and URL allowlists.

## Browser Controls

- CSP is delivered as an HTTP response header.
- The frontend avoids third-party scripts, external fonts, and npm packages.
- Provider secrets never enter browser-delivered code.

## Operational Limits

- The HTTP server has explicit read, write, idle, and header timeouts.
- Request bodies are globally bounded.
- Background work is leased through SQLite jobs with retry state.
