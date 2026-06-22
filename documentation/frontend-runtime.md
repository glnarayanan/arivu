# Frontend Runtime

The rewrite frontend is a dependency-free browser SPA served from the Go binary.

## Modules

- `index.html`: root document and script/style references.
- `styles.css`: design tokens, brutalist layout, responsive rules, and reduced-motion handling.
- `app.js`: router, API client, auth flow, primary screens, and local UI state.
- `app.js` UI primitives: toasts, modal dialogs, destructive confirmations,
  focus trapping, menu roving focus, settings tabs, escape handling, and route
  cleanup for global listeners.

## Runtime Rules

- Navigation uses same-origin links intercepted by the router.
- API calls use `fetch` with `credentials: "include"`.
- CSRF headers are injected from the `csrf_token` cookie when present.
- A single refresh retry is attempted after a protected API route returns 401.
- Interactive controls are native elements unless custom behavior is required.
- Route changes expose a top progress marker while async page work is pending.
- Form actions disable the initiating button and swap to specific busy labels.
- Toasts use semantic tones for success and error feedback.
- The authenticated shell includes a skip link and marks the active nav item with
  `aria-current="page"`.
- Custom dialogs use `role="dialog"`, `aria-modal`, focus restoration, Escape
  close, and tab containment.
- Menus use `aria-haspopup`, `aria-expanded`, `role="menu"`, roving
  `role="menuitem"` focus, Arrow key movement, and Escape close.
- Tabs use `role="tablist"`, `role="tab"`, `role="tabpanel"`, Arrow/Home/End
  keyboard behavior, and hidden inactive panels.
- Archived page HTML is inserted only after backend sanitization.

## Remaining Frontend Work

- Browser workflow tests for dashboard, settings, import, admin, mobile, and keyboard shortcuts.
- Visual comparison against the legacy brutalist UX.
- Creator-confirmed brand references for `documentation/design-context.md`.
