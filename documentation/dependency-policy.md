# Rewrite Dependency Policy

The rewrite minimizes supply-chain surface by defaulting to the Go standard
library and first-party browser code.

## Runtime Dependencies

- `github.com/mattn/go-sqlite3`: SQLite database driver.
- `golang.org/x/crypto`: Argon2id, HKDF key derivation, and legacy bcrypt verification.
- `golang.org/x/net/html`: HTML parser for the sanitizer.

The module targets Go 1.24 or newer so the patched `golang.org/x/crypto` and
`golang.org/x/net` lines can be used without carrying known vulnerable versions.

Provider SDKs are not used in the current rewrite. Gemini, Resend, and X are
called through narrow direct HTTP clients.

## Frontend Dependencies

The shipped frontend has no npm dependency tree. UI primitives, routing, API
calls, state, and motion are first-party browser JavaScript and CSS.

## Adding A Dependency

Only add a dependency when all are true:

1. The standard library or existing first-party code cannot reasonably provide the behavior.
2. The package replaces security-sensitive custom code or a large amount of fragile code.
3. The package has a narrow API surface and clear maintenance history.
4. The reason is documented here and in `CHANGELOG.md`.
