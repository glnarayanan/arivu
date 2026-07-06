# Frontend & Extensions

Arivu embeds all user interface assets within the standalone Go binary, presenting a seamless and lightweight experience that does not require node_modules or independent build configurations.

---

## Embedded Web Console

The UI assets are located in `/internal/app/web/` and are embedded directly into the Go server runtime binary using standard Go virtual filesystem constructs (`go:embed`).

### Assets
- `index.html`: The main console markup. Offers an accessible, clean document tree.
- `app.js`: Vanilla browser JavaScript modules implementing reactive client routing, form submissions, settings orchestration, and API connection.
- `styles.css`: Hardened stylesheet implementing structural layouts with support for theme scaling.
- `favicon.svg`: Icon asset.
- `manifest.webmanifest`: PWA metadata plus a GET share target for mobile/browser capture into the dashboard.
- `sw.js`: app-shell cache for offline startup; `/api/*` is network-only.

### Product Routes

- `/today`: default signed-in cockpit with the dated daily note, Inbox count,
  due/open work loops, Review items, recent notes, memory jogger, and links into
  the deeper Capture, Inbox, Focus, Review, Notes, and Assistant routes.
- `/dashboard`: capture-first cockpit for URL saves, quick notes, manual tags,
  PWA share-target prefill, visible processing status, saved-page search,
  voice dictation into the quick note, collapsible filters, and collapsible
  saved-search management.
- `/inbox`: capture triage for bookmarks and notes, with per-item stage,
  priority, next action controls, inline action items, bulk selection, and
  keyboard triage for stage changes.
- `/focus`: daily work surface combining action items and reminders with links
  back to their source items plus quick reminder completion, snooze, and delete
  actions. Views include pending, overdue, today, upcoming, and completed.
- `/assistant`: guided planner for inert assistant drafts plus pending
  proposals for allowlisted actions, with payload/result inspection and
  explicit execute/reject controls. Draft cards expose the source item and JSON
  payload before they can be queued as proposals.
- `/bookmark/:id`: sanitized reader-first workspace with summaries, read
  state, tags, a compact next-step workflow panel, and collapsed workbench
  groups for annotations, linked notes, explicit note links/backlinks, action
  items, reminders, related items, and review actions. Reminder controls include
  timezone-aware due times, recurrence, in-app/email channel selection, inline
  edits, snooze, completion, and deletion. Link selectors use slim
  `/api/link-targets` reads rather than full archive rows.
- Command palette: the global Actions button and `Cmd/Ctrl+K` expose route
  jumps, capture, note creation, search/cited answer, and current-item task,
  reminder, and link creation through existing APIs.
- `/notes`: compact standalone-note list. `/notes/:id` is the full note
  workspace for editing, action items, reminders, explicit links, backlinks,
  note-to-note links, and note-to-bookmark links. Reminder controls match
  bookmark reminder controls. Note link selectors also use `/api/link-targets`.
  `/notes?note=<id>` redirects to `/notes/:id` for compatibility.
- `/review`: daily review queue with complete and snooze actions, "why this
  came back" reason labels, priority metadata, and inline task/reminder
  controls. Review and cited-answer cards expose recall feedback controls that
  persist through `/api/feedback`.
- `/duplicates`: duplicate groups and merge workflow.
- `/knowledge-graph`: entity and concept overview from local extraction and optional provider embeddings.
- `/analytics`: summary counts, topics, and actionable insight signals.

### Asset Caching Performance
As detailed in `/internal/app/app.go`, static asset handlers set content ETags:
- `index.html` and SPA fallbacks use `Cache-Control: no-cache` so deploys can refresh the shell.
- JavaScript, CSS, SVG, and favicon responses use `Cache-Control: public, max-age=0, must-revalidate` so repeat visits can revalidate and receive `304 Not Modified`.

---

## Companion Browser Extension

The `/extension/` directory houses a companion, cross-browser compatible WebExtension.

### Manifest Layout
Declared in `manifest.json` as a Manifest V3 extension, providing integrations:
- `background.js`: Listens for action clicks, context-menu saves, keyboard command saves, selected-text capture, and secure token relay from the active Arivu backend instance.
- `content.js`: Injected scripts to extract selected page content and trigger seamless "Save in Arivu" operations from local tabs.
- `popup.html` & `popup.js`: Mini utility panel allowing users to connect their active local server instance, preserve page title, capture notes/tags/collections, and open the saved item or Inbox immediately after capture.

---

## CLI Integration Client

The compiled Arivu binary incorporates client interfaces to manage bookmarks securely directly from shell prompts.

When users call `./arivu login --email ... --password ...`, `/cmd/arivu/main.go`
stores the returned CLI tokens in the user's config directory. Later
`./arivu save`, `./arivu list`, and `./arivu search` calls reuse that saved
profile.
