# Frontend Runtime

The rewrite frontend is a dependency-free browser SPA served from the Go binary.

## Modules

- `index.html`: root document and script/style references.
- `styles.css`: design tokens, brutalist layout, responsive rules, and reduced-motion handling.
- `app.js`: router, API client, auth flow, primary screens, and local UI state.
- `favicon.svg`: small embedded SVG icon served by both `/favicon.svg` and
  legacy `/favicon.ico` requests.
- `app.js` UI primitives: toasts, modal dialogs, destructive confirmations,
  focus trapping, menu roving focus, settings tabs, escape handling, and route
  cleanup for global listeners.

## Runtime Rules

- Navigation uses same-origin links intercepted by the router.
- Routes declare public or protected access before rendering. Public auth-adjacent
  routes such as `/reset-password` and `/accept-invite` do not call
  `/api/auth/me`.
- Embedded static assets use content ETags and `Cache-Control:
  public, max-age=0, must-revalidate`, so repeat visits can revalidate JS, CSS,
  and SVG assets with 304 responses instead of downloading the full bodies.
- The SPA shell document uses `Cache-Control: no-cache` so route fallbacks stay
  fresh across binary deploys.
- API calls use `fetch` with `credentials: "include"`.
- CSRF headers are injected from the `csrf_token` cookie when present.
- A single refresh retry is attempted after a protected API route returns 401.
- Interactive controls are native elements unless custom behavior is required.
- Route changes expose a top progress marker while async page work is pending.
- Form actions disable the initiating button and swap to specific busy labels.
- Form failures render inline messages linked to the affected fields with
  `aria-describedby`.
- Toasts use semantic tones for success and error feedback; error toasts use
  assertive alert semantics.
- The authenticated shell includes a skip link and marks the active nav item with
  `aria-current="page"`.
- Custom dialogs use `role="dialog"`, `aria-modal`, focus restoration, Escape
  close, and tab containment.
- Menus use `aria-haspopup`, `aria-expanded`, `role="menu"`, roving
  `role="menuitem"` focus, Arrow key movement, and Escape close.
- Tabs use `role="tablist"`, `role="tab"`, `role="tabpanel"`, Arrow/Home/End
  keyboard behavior, and hidden inactive panels.
- Bookmark grid children use `content-visibility: auto` with an intrinsic size
  hint so long saved-item lists skip offscreen rendering work.
- Archived page HTML is inserted only after backend sanitization.

## Browser Smoke Checks

- Restart the Go server after frontend CSS or JS edits; assets are embedded in
  the running binary.
- Use a temporary SQLite database and `SIGNUPS_ENABLED=true` when checking
  first-run browser flows.
- Cover `/auth`, signup to `/dashboard`, settings tabs, Actions menu keyboard
  movement, save/search/detail/delete, reset-password, invite copy, and a
  390x844 mobile viewport.
- Keep console warning/error collection empty during completed checks.
- Keep screenshot artifacts out of the repository unless a test needs them.

## Remaining Frontend Work

- Browser workflow tests for dashboard, settings, import, admin, mobile, and keyboard shortcuts.
- Visual comparison against the legacy brutalist UX.
- Run `impeccable document` when the team wants a generated `DESIGN.md` to
  complement the existing `PRODUCT.md` context.
