# Frontend And Capture Clients

Arivu embeds a dependency-free browser SPA in the Go binary and ships companion
WebExtension and CLI capture clients. Production does not require Node.js or a
separate frontend build.

## Embedded Web Application

Assets live under `internal/app/web/` and are embedded with `go:embed`.

- `index.html`: accessible root document, white `#ffffff` theme-color
  metadata, app mount, route progress indicator, and toast region.
- `app.js`: client router, authenticated shell, API client, canonical and
  compatibility routes, shared UI primitives, and product screens.
- `route-lifecycle.mjs`: generation-scoped route commits, abort signaling, and
  route-owned listener cleanup that prevents stale asynchronous renders.
- `styles.css`: Brightlight-derived light-only tokens (white canvas, dashed
  rails, `--accent-500` pills), responsive shell, component states, graph
  semantics, and reduced-motion handling.
- `manifest.webmanifest`: install metadata, matching light-only `#ffffff`
  background/theme colors, and the compatible GET share target into
  `/dashboard`.
- `sw.js`: app-shell and first-party font cache; `/api/*` remains network-only.
- `service-worker-register.mjs`: CSP-compatible registration lifecycle.

The visual reference changes presentation only. Existing routes, navigation,
menu layout and options, content hierarchy, and interactions remain unchanged.
The shipped assets do not contain Brightlight's Astro components, Tailwind
configuration, scripts, font binaries, or images. Arivu independently bundles
official OFL-licensed Geist and Noto Serif WOFF2 files with license notices; the
browser client remains first-party and dependency-free.

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

The authenticated shell exposes exactly Home, Library, Notes, Graph, and
Insights as primary navigation. Capture and Search / Ask are persistent global
actions. Settings and administration live under the profile controls;
imports/exports remain a Settings section rather than a duplicate menu item.
The existing More / `Cmd/Ctrl+K` command palette remains.

## Compatibility Routes

The client copies the incoming query parameters before replacing the URL with a
canonical destination:

- `/dashboard` -> `/library?view=capture`
- `/knowledge-graph` -> `/graph`
- `/analytics` -> `/insights`
- `/inbox` -> `/library?view=inbox&stage=inbox`
- `/focus`, `/review`, `/board` -> matching `/today?view=...` contexts. Legacy
  Focus `view` filters are translated to the canonical `focus` query parameter
  so they do not collide with the Home context selector.
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
token. Popup requests cross a message seam into the background worker, which
owns API URL joining, bearer headers, JSON transport, normalized errors, and
401 token cleanup. The broker accepts only the extension popup and exactly the
collection-list and bookmark-create operations. Permission prompts remain
popup-owned because optional origins require a user gesture. Arivu-origin pages
continue using the native reader annotation composer.

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
