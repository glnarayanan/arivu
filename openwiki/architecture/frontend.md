# Frontend And Capture Clients

Arivu embeds a dependency-free browser SPA in the Go binary and ships companion
WebExtension and CLI capture clients. Production does not require Node.js or a
separate frontend build.

## Embedded Web Application

Assets live under `internal/app/web/` and are embedded with `go:embed`.

- `index.html`: accessible root document, theme-color metadata, app mount, route
  progress indicator, and toast region.
- `app.js`: client router, authenticated shell, API client, canonical and
  compatibility routes, shared UI primitives, and product screens.
- `styles.css`: quiet-cartographic tokens, light/dark palettes, responsive shell,
  component states, graph semantics, and reduced-motion handling.
- `manifest.webmanifest`: install metadata and the compatible GET share target
  into `/dashboard`.
- `sw.js`: app-shell cache; `/api/*` remains network-only.
- `service-worker-register.mjs`: CSP-compatible registration lifecycle.

## Canonical Product Routes

- `/today`: Home knowledge pulse and contextual Focus, Review, and Board views.
- `/library`: unified, cursor-based browsing of bookmarks, notes, daily notes,
  annotations, knowledge objects, entities, and concepts. Filters cover type,
  topic, source, stage, date, and connection state.
- `/graph`: bounded typed graph from `/api/knowledge-graph/v2`, with focused
  expansion, provenance, confidence, inspector, SVG keyboard selection, and an
  equivalent accessible node list.
- `/insights`: deterministic, evidence-backed learning patterns with time
  window, confidence, detection rationale, evidence links, next actions, and
  feedback.
- `/search`: keyword retrieval and cited Ask over saved Arivu content.
- `/bookmark/:id` and `/notes/:id`: existing detailed knowledge workspaces with
  annotations, tasks, reminders, explicit links, backlinks, and related items.
- `/settings` and `/admin`: account, import/export, providers, connections, and
  administrator controls.

The authenticated shell exposes exactly Home, Library, Graph, and Insights as
primary navigation. Capture and Search / Ask are persistent global actions.
Notes, imports/exports, settings, and administration live under the profile or
contextual controls. The existing More / `Cmd/Ctrl+K` command palette remains.

## Compatibility Routes

The client copies the incoming query parameters before replacing the URL with a
canonical destination:

- `/dashboard` -> `/library?view=capture`
- `/knowledge-graph` -> `/graph`
- `/analytics` -> `/insights`
- `/inbox` -> `/library?view=inbox&stage=inbox`
- `/focus`, `/review`, `/board` -> matching `/today?view=...` contexts
- `/assistant` -> `/search?mode=ask&review=actions`
- `/objects` -> `/library?type=knowledge_object`
- `/evolution` -> the changed-thinking Insights context with the existing
  evolution view retained
- `/duplicates` -> `/library?management=duplicates`
- `/imports` -> `/settings?section=import`

PWA share URLs continue targeting `/dashboard`; the compatibility redirect
preserves shared `title`, `text`, and `url` parameters before capture renders.

## Companion Browser Extension

The Manifest V3 extension in `extension/` keeps extension tokens in its worker
and supports action clicks, context menus, keyboard saves, popup capture, and
opt-in selected-text annotations. Page/link saves post to
`POST /api/extension/bookmarks`; selected text posts `{url,title,quote,note}` to
`POST /api/extension/annotations`. Both endpoints require an extension-audience
token. Arivu-origin pages continue using the native reader annotation composer.

## CLI And Agent Interfaces

`arivu login`, `save`, `list`, and `search` reuse CLI-audience sessions stored in
the user's config directory. The additive knowledge APIs do not change these
contracts. Agent routes continue supporting scoped search, bookmark/note reads,
note creation, tasks, reminders, and decision recording.

## Asset Delivery

The SPA shell uses `Cache-Control: no-cache`. Static assets expose ETags and
revalidate. The service worker pre-caches the canonical knowledge routes and the
legacy Dashboard share target, deletes stale cache versions on activation, and
never caches authenticated API responses.
