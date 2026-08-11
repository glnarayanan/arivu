# Dependency Policy

The rewrite minimizes supply-chain surface by defaulting to the Go standard
library and first-party browser code.

## Runtime Dependencies

- `github.com/mattn/go-sqlite3`: SQLite database driver.
- `golang.org/x/crypto`: Argon2id, HKDF key derivation, and legacy bcrypt verification.
- `golang.org/x/net/html`: HTML parser for the sanitizer.

The module targets Go 1.25 or newer so the patched `golang.org/x/crypto` and
`golang.org/x/net` lines can be used without carrying known vulnerable versions.

Provider SDKs are not used in the current rewrite. Model providers, Resend, and
X are called through narrow direct HTTP clients.

## Supply-Chain Checks

- CI runs `go mod verify` before tests to check downloaded module checksums.
- CI runs `go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...` with a pinned
  scanner version.
- CI uploads a short-lived release evidence bundle containing the Linux build,
  `go version -m` build info, `go list -m -json all` module inventory, and
  SHA-256 checksums. This keeps artifact provenance inspectable without adding
  a production dependency or a separate SBOM generator.
- Release tags publish Linux AMD64 and ARM64 `arivu`, `arivu-installer`, and
  native capture runtime artifacts, plus the bootstrap `install.sh`, build
  info, module inventory, `SHA256SUMS`, and provenance attestations.
- The bootstrap installer verifies release `SHA256SUMS` before executing
  `arivu-installer`. The installer verifies the Arivu binary checksum before
  replacing `/usr/local/bin/arivu`. Release attestations are still published for
  independent verification, but the target host does not need GitHub CLI auth.
- CI runs first-party browser JavaScript syntax checks and the extension
  URL/origin self-test with the workflow's pinned Node runtime; Arivu
  still ships no npm dependency tree.
- Dependabot monitors Go modules, GitHub Actions, and capture npm packages
  weekly.
- The scheduled documentation workflow pins both the OpenWiki CLI version and
  the third-party pull-request action commit; generated documentation may
  update ordinary `openwiki/` pages and the lean `AGENTS.md`, but cannot rewrite
  the workflow that receives repository write permissions or the policy pages
  documenting that boundary.

## Frontend Dependencies

The shipped frontend has no npm dependency tree. UI primitives, routing, API
calls, state, and motion are first-party browser JavaScript and CSS.
The Brightlight-derived presentation is implemented from visual principles
only; its Astro/Tailwind implementation, scripts, font binaries, images, and
other reference assets are not shipped or required. Arivu independently
self-hosts the official OFL-licensed Geist and Noto Serif WOFF2 files under
`internal/app/web/fonts`, alongside their license notices; no font CDN or npm
runtime dependency is required.
The extension popup also uses native system font stacks and does not import
remote font CSS.

## Isolated Capture Bundle

The optional `capture/` service is the only production boundary allowed to
carry browser and DOM dependencies. It is not linked into the Go binary and
nothing from it is imported by the embedded frontend. Its approved direct
dependencies are pinned in `capture/package.json` and its lockfile:

- Playwright `1.62.1` with its pinned Chromium runtime.
- Mozilla Readability `0.6.0`.
- JSDOM `30.0.1`.
- Monolith `2.10.1` as a separately pinned executable.

They are justified because a real browser, a mature article projection, an
HTML DOM, and a self-contained archive engine are materially harder and less
safe to recreate in the core application. CI installs from the lockfile, runs
the capture protocol suite, and audits production npm dependencies. Updates
require explicit approval plus security, integration, and capture-corpus review.
Production releases bundle the pinned Node executable, Playwright Chromium,
node modules, and Monolith into one architecture-specific archive. The installer
is the only supported production lifecycle: it verifies, installs, upgrades,
disables, and rolls back that archive without exposing npm or Docker to users.
Both architectures build on the declared Ubuntu 22.04 compatibility baseline,
matching the complete runtime's Ubuntu 22.04+ and Debian 12+ support policy.

## Adding A Dependency

Only add a dependency when all are true:

1. The standard library or existing first-party code cannot reasonably provide the behavior.
2. The package replaces security-sensitive custom code or a large amount of fragile code.
3. The package has a narrow API surface and clear maintenance history.
4. The reason is documented here and in `CHANGELOG.md`.
