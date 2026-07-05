# Security & Defense Workflows

Arivu is built with a zero-trust design for local and self-hosted environments. It contains explicit protections against common application security vectors.

---

## Authentication & Session Lifecycles

Authenticating with Arivu involves secure OAuth 2.0 flow states or localized custom passwords. The identity runtime is defined in `/internal/auth/auth.go`.

### Audience Enforcement

Arivu issues cryptographically secure, random session tokens (`/internal/ids/ids.go`) and assigns each an explicit **Audience** context:

- `web`: Browser session accessing the embedded main console. Must protect against CSRF.
- `cli`: Terminal interface sessions. Excluded from cookie-related CSRF.
- `extension`: Companion browser extension. Uses bespoke authorization structures.

Tokens are stored using SHA-256 digests in the backend SQLite `sessions` table. During authentication, raw user password hashing is dynamically updated from legacy standard `bcrypt` algorithms to modern, highly cost-tunable `Argon2id` derivations.

### CSRF Protections
Browser requests (`web` audience actions) utilize HTTP-only, secure cookies. Any mutating endpoint (POST, PUT, DELETE) requires the client to fetch, hold, and submit a session-matched CSRF protection token header.

---

## SSRF Prevention (`safefetch`)

One of Arivu's core features is crawling user-saved URLs to index them. This presents a major Server-Side Request Forgery (SSRF) risk. Arivu safeguards outbound requests using a hardened fetch engine in `/internal/safefetch/safefetch.go`.

### Hardened Dialer Protections

1. **Host & DNS Resolution Validation**: The custom dialer resolves DNS names and analyzes the destination IP addresses *before* making the physical handshake.
2. **Private Address Boundary Filtering**: Rejects any address falling into local or private blocks:
   - Loopback: `127.0.0.0/8`, `::1/128`
   - Private subnets (RFC 1918): `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
   - Link-local: `169.254.0.0/16`, `fe80::/10`
   - Multicast/Unspecified ranges.
3. **Environmental Proxy Erasure**: Forces a nil proxy configuration to prevent outgoing requests from bypassing checks through environment-scoped variables or local loopbacks.
4. **Redirect Validation**: Evaluates target redirects sequentially. Every hop must resolve to a valid, public IP address or the request immediately aborts.
5. **Content-Type & Size Limits**: Strict checks verify return payload types (rejecting large, binary files or non-HTML documents) and enforce a concrete reading limit (e.g., max 10MB) to prevent decompression bombs or disk fatigue.

---

## Backend-Owned HTML Sanitization

Users import bookmarks containing arbitrary rich documentation or raw source HTML. Rendering arbitrary elements exposes others to Cross-Site Scripting (XSS).

- **Implementation**: `/internal/sanitize/sanitize.go`.
- **Strategy**: Utilizes a strict element and attribute allowlist on the Go backend server before persistence.
- **Allowed Elements**: Text tags (`p`, `br`, `b`, `i`, `strong`, `em`, `code`, `pre`, `ul`, `ol`, `li`, `h1`-`h6`, `blockquote`).
- **Forbidden Elements**: Any scripting constructs (`script`, `iframe`, `object`, `embed`, etc.) or execution handlers (`onload`, `onerror`) are completely scrubbed.
- **URL Schemes**: Sanitizes `href` protocols, strictly validating only `http://` and `https://` linkages. `javascript:` or data-based inputs are stripped.

---

## Runtime Secrets Encryption

Runtime provider settings are stored in SQLite.

- `/internal/runtimeconfig` resolves settings from SQLite, environment variables, and defaults.
- `/internal/secrets/secrets.go` encrypts provider secrets before they are stored in the SQLite `settings` table.
- Plain settings such as redirect URI, Resend sender, and X enablement are stored in `value_plain`.
