# Arivu capture service

This directory is the only production bundle allowed to depend on a browser or
DOM implementation. The Go application and its embedded frontend remain free of
Node and browser dependencies.

The service listens on a Unix socket and handles captures entirely in the
background. It never opens a visible user tab or window. Each attempt launches
a fresh headless Chromium process and browser context, renders the public page,
extracts a reader projection with Mozilla Readability and JSDOM, optionally
creates a JPEG screenshot and PDF, and passes the rendered DOM through Monolith
for a self-contained HTML archive.

## Approved production dependencies

Direct package versions are exact in `package.json` and `package-lock.json`:

- Playwright `1.61.1` and its pinned Chromium runtime (Apache-2.0).
- Mozilla Readability `0.6.0` (Apache-2.0).
- JSDOM `29.1.1` (MIT).
- Monolith `2.10.1` (CC0-1.0), installed as a separate executable.

No dependency from this bundle may be imported into `internal/app/web`.
Dependency updates require the same explicit approval, audit, integration test,
and capture-corpus review as adding a production dependency.

## Runtime contract

The service requires:

```sh
ARIVU_CAPTURE_SOCKET=/run/arivu-capture/helper.sock
ARIVU_CAPTURE_RUNTIME_DIR=/run/arivu-capture/attempts
ARIVU_MONOLITH_PATH=/usr/local/lib/arivu-capture/monolith
node capture/src/index.mjs
```

Use Node `22.13.0` or newer. Production packaging pins Chromium through
Playwright and pins Monolith to `2.10.1`; it must not resolve either runtime to
an unreviewed floating version.

The app and helper must share a dedicated group that can traverse the runtime
directory and connect to its mode-`0660` Unix sockets. The helper should run as
its own unprivileged user with a private temporary directory. Do not place
unrelated users or services in the capture group.

For every request, Go creates a random authenticated egress proxy inside a
random attempt directory. Chromium and Monolith reach the network only through
that proxy. Go performs DNS resolution, blocks non-public and rebinding targets,
limits HTTP/CONNECT destinations, and enforces per-response and aggregate byte
budgets. The helper validates that the proxy socket resolves beneath the
operator-configured runtime directory before connecting.

The helper returns one bounded newline-terminated JSON manifest followed by
raw payloads in deterministic order: reader HTML, reader text, requested
artifacts, then reader images. It never chooses filesystem paths in the Go
process. Go validates the full manifest first, copies exact byte counts into its
own mode-`0700` directory, rejects truncation and trailing bytes, and removes
the directory after ingestion.

Monolith receives the browser-rendered DOM on stdin with the final page URL as
its base. Archives use isolation and omit JavaScript, audio, video, frames, and
remote fonts. Monolith receives the same per-attempt authenticated proxy and an
explicit deadline; archive failure is partial and does not discard a usable
reader projection.

## Development and verification

```sh
cd capture
npm ci
npx playwright install chromium
npm test
npm audit --omit=dev

cd ..
ARIVU_CAPTURE_INTEGRATION=1 go test ./internal/browsercapture \
  -run TestCaptureHelperIntegration -count=1 -v
```

The integration test uses a deterministic Monolith test double. Release and
installer validation must additionally exercise the pinned Linux Monolith
binary and the synthetic capture corpus.
