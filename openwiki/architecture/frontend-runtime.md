# Frontend Runtime

The rewrite frontend is a dependency-free browser SPA served from the Go binary.

## Modules

- `index.html`: root document and script/style references.
- `styles.css`: design tokens, brutalist layout, responsive rules, and reduced-motion handling.
- `app.js`: router, API client, auth flow, primary screens, and local UI state.
- `favicon.svg`: small embedded SVG icon served by both `/favicon.svg` and
  legacy `/favicon.ico` requests.
- `manifest.webmanifest`: installable PWA metadata and GET share-target
  definition for mobile/browser capture into `/dashboard`.
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
- Route changes update the document title, announce the active route, and move
  focus to the main content after internal navigation or browser history
  navigation.
- The Actions button and `Cmd/Ctrl+K` open the command palette. Palette commands
  reuse existing bookmark, note, search, action item, reminder, link, and
  link-target APIs; current-item commands appear only on bookmark and note
  detail routes.
- Form actions disable the initiating button and swap to specific busy labels.
- Form failures render inline messages linked to the affected fields with
  `aria-describedby`.
- Toasts use semantic tones for success and error feedback; error toasts use
  assertive alert semantics.
- The authenticated shell includes a skip link and marks the active nav item with
  `aria-current="page"`.
- `/today` is the default signed-in route. It reads `/api/daily-notes/{date}`,
  `/api/inbox`, `/api/action-items`, `/api/reminders`, `/api/review`,
  `/api/memory-jogger`, and `/api/notes`, then saves the dated daily note with
  `PUT /api/daily-notes/{date}`.
- `/dashboard` pre-fills the save form from PWA share-target `title`, `text`,
  and `url` query parameters. The URL field prefers the explicit `url`
  parameter, then falls back to the first URL found in shared text.
- Dashboard retrieval supports query, tag, domain, source, read-status, and
  saved-date range filters, saving the current search, replaying saved searches,
  and a cited answer panel sourced only from matching saved items. Cited answers
  synthesize from saved summaries, highlights, snippets, and standalone notes
  while keeping citations back to the source items. Bookmark-only filters such
  as tag, domain, source, and read status keep results bookmark-scoped.
- `/notes` is the compact standalone-note list. `/notes/:id` is the full note
  workspace for editing, action items, reminders, explicit links, backlinks,
  note-to-note links, and note-to-bookmark links. Link selectors call
  `/api/link-targets` for slim id/title target rows instead of loading full note
  bodies or bookmark archive content. `/notes?note=<id>` redirects to
  `/notes/:id` for compatibility.
- Settings import/export uses native controls: paste supported export content,
  submit it to `/api/bookmarks/import`, inspect recent import jobs with fetched,
  AI-processed, failed, completed status counters, source report chips, and
  bounded item provenance for the import just submitted, download or restore
  full JSON backups with second-brain data, or download CSV, browser HTML, and
  Markdown bookmark interchange exports. Obsidian ZIP export downloads
  vault-ready bookmark and note folders with explicit graph links as wikilinks
  from the same export route.
- Settings tags uses native forms to create primary tags and add aliases to
  existing tags through the normalized tag APIs.
- Settings profile uses the existing profile and password-change routes; Settings
  API keys uses the existing admin key status/update routes and shows an
  admin-required message for non-admin users.
- Settings connections exposes X status, connect, sync, and disconnect controls
  through the existing X OAuth and sync routes.
- `/admin` exposes overview, API usage, user management, system, activity,
  collections, and audit sections for admin users.
- Extension popup capture includes collection, quick note, and comma-separated
  tag fields while context-menu capture keeps the selected-text quote path.
- Bookmark save responses include `job_id`; the dashboard shows a short
  processing status before navigating to the saved bookmark.
- Bookmark detail is now reader-first: sanitized archived HTML, summaries,
  tags, and source controls stay up top; workflow state gets one next-step
  panel; annotations, notes, links, tasks, reminders, and related items live in
  disclosure groups. The annotation form can copy the current browser selection
  into the quote field when the selection is fully inside the sanitized reader
  content, and existing annotations can be edited or deleted inline.
- `/focus` keeps the pending default and adds overdue, today, upcoming, and
  completed views over action items and reminders.
- `/review` includes the daily memory card from `/api/memory-jogger`, the
  priority-sorted review queue from `/api/review`, complete and snooze actions,
  reason labels, priority metadata, and inline task/reminder controls.
  Standalone notes open in `/notes/:id` and do not expose bookmark-only archive
  controls.
- `/review`, `/duplicates`, `/knowledge-graph`, and `/analytics` are real
  product routes, not placeholders.
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
- Cover `/dashboard`, `/inbox`, `/focus`, `/review`, `/assistant`, `/notes`,
  `/notes/:id`, `/bookmark/:id`, and `/settings` at desktop and 390x844 mobile
  sizes after second-brain route changes.
- Keep console warning/error collection empty during completed checks.
- Keep screenshot artifacts out of the repository unless a test needs them.

## Ongoing Frontend Verification

- Keep source-contract tests in `/internal/app/app_test.go` aligned with every
  route-level UI capability that is not yet covered by a dedicated browser test.
- Re-run browser smoke checks whenever route structure, dense controls, or
  keyboard shortcuts change.
