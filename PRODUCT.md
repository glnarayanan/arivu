# Arivu Product

Arivu is a self-hosted second brain that turns captured material into connected
knowledge and visible learning patterns. It is private by default, runs from a
single Go binary with SQLite, and remains useful without an AI provider.

## Core Loop

**Capture -> Connect -> Discover -> Learn**

- **Capture:** save a link, note, quote, or file without choosing a folder,
  taxonomy, or provider first.
- **Connect:** add explicit links and backlinks; review locally derived concept,
  entity, source, and similarity relationships.
- **Discover:** search across saved material or explore a bounded knowledge
  graph with provenance and confidence.
- **Learn:** revisit useful material and act on deterministic, evidence-backed
  patterns such as emerging themes, recurring connections, forgotten value,
  knowledge gaps, and serendipitous connections.

Explicit user links are canonical. Derived relationships and insights are
rebuildable. Feedback about those derivatives is durable and user-scoped.

## Primary Experience

The authenticated interface has five primary destinations:

- **Home** (`/today`) is a knowledge pulse: daily note, active work, new
  material, useful memories, and contextual Focus, Review, and Board views.
- **Library** (`/library`) browses bookmarks, notes, daily notes, annotations,
  knowledge objects, entities, and concepts. It supports cursor pagination and
  filters for type, topic, source, stage, date, and connection state.
- **Notes** (`/notes`) is the primary writing workspace for standalone notes,
  note details, tasks, reminders, and explicit connections to saved material.
- **Graph** (`/graph`) renders a bounded, focusable map from typed graph nodes
  and edges. It includes provenance, confidence, an inspector, and an
  equivalent keyboard and screen-reader-friendly node list.
- **Insights** (`/insights`) explains deterministic learning patterns, cites
  owned evidence, exposes detector confidence and time windows, and accepts
  Useful, Not useful, Snooze, and Dismiss feedback.

Capture and Search / Ask are globally available. Search has the canonical route
`/search`; cited answers continue to use only saved Arivu content.

Existing deep links remain compatibility entry points and preserve query state:

- `/dashboard` -> Library capture
- `/knowledge-graph` -> Graph
- `/analytics` -> Insights
- `/inbox` -> filtered Library
- `/focus`, `/review`, `/board` -> contextual Home views
- `/assistant` -> Search / Ask action review
- `/objects` -> knowledge objects in Library
- `/evolution` -> the corresponding Insights context
- `/duplicates` -> Library maintenance

Bookmark and note detail URLs, settings, administration, imports, exports,
extension routes, CLI routes, and agent routes remain stable.

## Users And Progressive Depth

Arivu serves individual self-hosters, readers, researchers, and operators,
including multi-user private instances. Beginners can capture and retrieve
without setup. Regular users can develop notes and explicit connections.
Advanced users can focus the graph, inspect provenance, and investigate
patterns. Tasks and reminders support knowledge work but do not define it.

## Product Guarantees

- Capture never requires AI, classification, tags, folders, or graph upkeep.
- Local extraction, text search, explicit links, deterministic enrichment,
  graph structure, and deterministic insights continue without a provider.
- Optional providers may improve summaries, embeddings, explanations, and
  synthesis; they must not invent unsupported evidence.
- Every query and derivative is scoped to the authenticated user.
- Archived HTML is sanitized server-side and outbound fetching remains
  SSRF-shielded.
- Web, CLI, and extension sessions remain audience-isolated.
- Existing data, backups, imports, exports, PWA share capture, offline capture,
  browser extension behavior, CLI behavior, and administration remain intact.
- The shipped frontend stays dependency-free and native-browser-first.

## Current Knowledge Model

The unified Library and Graph project existing durable content into nodes:
bookmark, note, daily note, annotation, knowledge object, entity, and concept.
Graph edges include explicit links, source relationships, shared concepts,
shared entities, and semantic similarity when embeddings exist. Responses carry
stable IDs, types, provenance, and confidence where relevant.

`knowledge_feedback` stores user feedback for insight and relationship targets.
Dismissed or snoozed derivatives are hidden from subsequent responses.
Confirming a relationship creates a durable explicit link only when both
endpoints are owned bookmark or note items and the submitted edge matches the
server-owned relationship.

## Boundaries

Real-time collaboration, social publishing, a plugin marketplace, native mobile
apps, and full project management are outside the current product. Arivu remains
self-hosted, privacy-first, dependency-light, and centered on personal
knowledge even on multi-user instances.

The visual and interaction system is defined in `DESIGN.md`.

The Brightlight-derived presentation is a look-and-feel layer only. Arivu is
light-only, and the visual overhaul does not change routes, navigation or menu
options, information architecture, or product behavior. The Astro/Tailwind
reference implementation and its assets are not part of the shipped product.
