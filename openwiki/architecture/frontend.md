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

### Product Routes

- `/dashboard`: bookmark list, search, URL capture, quick note, manual tags, PWA share-target prefill, and visible processing status.
- `/inbox`: capture triage for bookmarks and notes, with per-item stage, priority, next action controls, and inline action items.
- `/assistant`: approval ledger for allowlisted assistant action proposals, with payload/result inspection and approve/reject controls.
- `/bookmark/:id`: sanitized reader view, summaries, processing state, read state, tags, related items, annotations, linked notes, explicit note links/backlinks, action items, reminders, and review actions.
- `/notes`: standalone notes for ideas and snippets that are not tied to a URL.
  `/notes?note=<id>` focuses a specific note from Inbox, backlinks, or tasks.
- `/review`: resurfacing-backed daily review queue with complete and snooze actions.
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
