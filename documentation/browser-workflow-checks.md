# Browser Workflow Checks

This page records the no-new-dependency browser checks for the Go rewrite.
The shipped app stays dependency-free; browser automation is run through the
Codex in-app Browser plugin rather than a checked-in Playwright dependency.

## Audit Fix Pass

Date: 2026-06-22

Target:

```bash
GOCACHE=/private/tmp/arivu-build-cache \
ARIVU_DB=/private/tmp/arivu-audit-fixes.sqlite3 \
SECRET_KEY=audit-fix-browser-secret-key-32-bytes \
SIGNUPS_ENABLED=true \
go run ./cmd/arivu serve -addr 127.0.0.1:18080
```

Covered workflows:

- `/reset-password` renders as a signed-out route without redirecting to
  `/auth`.
- `/reset-password?token=sample-token` renders the new-password form and
  submits to the existing reset API.
- `/accept-invite` renders as a signed-out route and offers the
  admin-mediated email plus temporary-password sign-in path.
- `/auth` exposes the forgot-password entrypoint.
- Auth and save form API failures render inline form messages and assertive
  error toasts.
- Desktop and mobile checks confirmed the quieter UI retains the archive
  aesthetic with reduced shadows, thinner borders, OKLCH tokens, and no global
  horizontal clipping.

## Polish Pass

Date: 2026-06-22

Target:

```bash
GOCACHE=/private/tmp/arivu-build-cache \
ARIVU_DB=/private/tmp/arivu-polish.sqlite3 \
SECRET_KEY=polish-browser-secret-key-32-bytes \
SIGNUPS_ENABLED=true \
go run ./cmd/arivu serve -addr 127.0.0.1:18080
```

Covered workflows:

- `/auth` renders labelled email/password fields and the updated sign-in/create-account controls.
- Signup with a temporary local user redirects to `/dashboard`.
- `/dashboard` renders the skip link, active shell navigation, save form,
  labelled search landmark, and empty bookmark state.
- The global Actions menu opens with `role="menu"`, focuses Dashboard first,
  moves roving focus to Settings with ArrowDown, and closes on Escape.
- `/settings` renders the tablist and ArrowRight moves selection from Profile
  to Import with the matching panel.
- Mobile-width validation at 390x844 confirmed no horizontal overflow and no
  measured button/link/input control smaller than 44x44.
- Console warning/error log collection was empty throughout the pass.
- Desktop and mobile screenshots were inspected manually; generated screenshot
  artifacts were not retained in the repository.

Implementation note:

- Restart the Go server after frontend CSS/JS edits when browser-checking local
  changes; the shipped assets are embedded into the running binary.

## Current Pass

Date: 2026-06-15

Target:

```bash
GOCACHE=/private/tmp/arivu-build-cache \
ARIVU_DB=/private/tmp/arivu-browser-workflow-3.sqlite3 \
SECRET_KEY=browser-workflow-secret-key-32-bytes \
SIGNUPS_ENABLED=true \
go run ./cmd/arivu serve -addr 127.0.0.1:18080
```

Covered workflows:

- `/auth` signup with a first user redirects to `/dashboard`.
- First-run `/api/bookmarks` returns `[]`; the dashboard renders the empty state
  instead of throwing on `null`.
- The dashboard save form, search input, and bookmark cards render as live
  controls, not escaped HTML text.
- Saving a bookmark, searching for it, opening the detail route, and opening the
  destructive delete dialog all work from the embedded frontend.
- Delete dialog exposes `role="dialog"`, traps focus, and closes on Escape.
- `/settings?section=import` selects the Import tab on load.
- Settings tabs support ArrowRight roving to Connections.
- The global Actions menu opens with `role="menu"`, focuses the first menu item,
  moves focus with ArrowDown, and closes on Escape.
- Console warning/error log collection was empty during the completed checks.
- Mobile-width DOM validation at 390x844 confirmed settings tabs still render as
  live controls and no escaped route markup is visible.

Known tooling limitation:

- The in-app Browser CDP screenshot command timed out in this run, and the
  element screenshot helper is not supported by the current plugin bridge. The
  workflow therefore records DOM, keyboard, URL, and console evidence here. A
  future CI job may add screenshot artifacts if the project accepts a test-only
  browser dependency.

## Regression Coverage

`internal/app/app_test.go` includes `TestBrowserFacingFirstRunContracts`
to keep the first-run bookmark response and embedded shell escaping behavior
covered by the normal Go test suite.
