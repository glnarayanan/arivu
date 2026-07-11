# Frontend Runtime

The embedded frontend is a dependency-free browser SPA served by the Go binary.
See `DESIGN.md` for its authoritative visual and interaction rules.

## Router And Shell

Routes declare public or protected access before rendering. Same-origin links
use history navigation; route changes update the document title, announce the
active surface, expose loading progress, and restore focus to main content.

The primary shell contains Home, Library, Graph, and Insights only. Desktop uses
a left rail, tablet a compact rail, and mobile a fixed four-item bottom bar.
Capture and Search / Ask remain visible globally. More opens the existing
command palette and the profile control opens Notes, imports/exports, Settings,
Administration for admins, and logout.

Legacy product routes run through `compatibilityRedirect`, which preserves the
incoming query string and supplies only missing canonical-view defaults. This is
load-bearing for saved deep links and the PWA share target.

## API Client And Offline Behavior

The client uses same-origin `fetch` with credentials, injects the readable CSRF
cookie into mutating requests, and attempts one session refresh after a 401.
Errors use inline form messages or semantic toasts.

Successful authenticated reads write bounded local snapshots for supported
existing surfaces. When a later read cannot reach Arivu, those views may show a
recent offline copy. Link and quote capture continue using the bounded offline
bookmark queue; note and file writes still require the server.

The service worker caches the app shell and canonical route documents but
bypasses `/api/*`. Online shell requests revalidate so deployments do not leave
stale JavaScript active.

## Knowledge Surfaces

### Home

`/today` combines the dated daily note with Inbox counts, open tasks/reminders,
review candidates, recent notes, and memory-jogger content. `view=focus`,
`view=review`, and `view=board` preserve the deeper existing workflows inside
the Home destination.

### Library

`/library` calls `/api/library/items` with a default page size of 48. Filters map
directly to the additive API. Cursor links retain active filters. The Library
Capture action opens the adaptive composer; the compatible Dashboard and Inbox
views retain their richer existing controls.

Object creation presents native fields selected by object type. It serializes
those values into the existing `fields` API object without exposing a raw JSON
editor in normal use.

### Search / Ask

`/search` uses `/api/search/items` for typed retrieval and `/api/search/answer`
for cited Ask. It preserves why-shown metadata and links results back to their
source workspace.

### Graph

`/graph` requests up to 48 nodes and 160 edges from
`/api/knowledge-graph/v2`; server limits remain authoritative. A focus selector
requests depth-one expansion. The SVG distinguishes node types and explicit vs
derived edges, while the inspector exposes provenance and confidence.

SVG nodes are keyboard-selectable. The open `Accessible node list` contains the
same nodes as ordinary buttons and does not require interpreting the visual
map. On constrained screens the canvas scrolls and the inspector moves below
it. Normal browser and touch zoom remain enabled; there are no custom zoom
controls or unbounded global render.

### Insights

`/insights` requests deterministic patterns and may filter them client-side by
family. Cards show title, explanation, detector window, confidence,
why-detected text, owned evidence, and relevant next actions. Feedback posts to
`/api/feedback` with an insight target.

Relationship Confirm/Dismiss uses the same endpoint with a relationship target.
Confirm can promote a server-verified bookmark/note derivative into an explicit
link; the browser does not construct a trusted edge by itself.

## Shared Interaction Rules

- Forms retain labels, inline errors, native validity, and specific busy states.
- Dialogs trap focus, close with Escape, and restore focus.
- Menus use ARIA menu semantics and roving keyboard focus.
- Tabs use tablist/tab/tabpanel semantics and Arrow/Home/End behavior.
- Toasts announce success politely and errors assertively.
- Archived third-party HTML appears only after backend sanitization.
- `prefers-color-scheme` selects the neutral/cobalt light or dark palette.
- `prefers-reduced-motion` disables nonessential transitions and animation.

## Browser Smoke Matrix

Use a temporary SQLite database with signups enabled. Check authenticated Home,
Library, Search / Ask, Graph, Insights, bookmark detail, note detail, Settings,
and every compatibility route.

Run desktop, tablet, and 390x844 mobile passes in light and dark mode. Include
keyboard-only navigation, graph/list equivalence, reduced motion, offline
capture, cached reads, empty/loading/failure states, long text, no horizontal
overflow, and an empty completed-flow console. Browser screenshots remain
temporary review artifacts unless a regression test requires them.
