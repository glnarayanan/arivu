# Second-Brain Loop

Arivu's product loop is **Capture -> Connect -> Discover -> Learn**.

## Capture

Capture is globally available from every authenticated screen. The adaptive
composer accepts a link, note, quote, or file. A capture does not require a tag,
folder, object type, workflow stage, classification result, embedding, or model
provider.

Link and quote captures save through the existing bookmark endpoint and can use
the browser's offline queue. Notes use the existing notes endpoint. Files use
media import and become searchable notes. Browser-extension, PWA share-target,
CLI, and agent capture contracts remain unchanged.

## Connect

Bookmark and note workspaces expose explicit links and backlinks. Explicit user
links remain canonical durable knowledge. Graph v2 also projects source,
concept, entity, and optional semantic-similarity relationships with provenance
and confidence.

Derived relationships are rebuildable. Confirming an eligible bookmark/note
relationship creates an explicit link only after the server verifies the target
edge and both owned endpoints. Dismissed relationship IDs are stored in
`knowledge_feedback` and filtered from later graph responses.

## Discover

Library unifies bookmarks, notes, daily notes, annotations, knowledge objects,
entities, and concepts with cursor pagination and practical filters. Search
retrieves saved material by text and structured context. Cited Ask synthesizes
only from saved Arivu content.

Graph starts with a bounded recent or focused view. Focus expansion follows
local relationships rather than loading an unbounded global network. The visual
SVG and accessible node list expose the same node set.

## Learn

Insights are deterministic local patterns with supporting evidence. Current
families are emerging themes, recurring connections, forgotten value, knowledge
gaps, and serendipitous connections. Each insight includes an explanation,
time window, confidence, why-detected text, owned evidence, and next actions.

Useful, Not useful, Snooze, and Dismiss feedback is durable and user-scoped.
Dismissed and active snoozed insights do not return. Optional AI remains
available on the legacy analytics path for extra explanation, but the canonical
Insights experience does not depend on it.

Analytical families use source publication time, publisher diversity, specific
supported concepts, and visible evidence. Evidence strength is not a model
probability. Forgotten value and knowledge gaps are typed recommendations and
remain separate from synthesized patterns.

## Home Contexts

Home (`/today`) is the daily knowledge pulse. It keeps the dated daily note and
surfaces new material, active work, review candidates, recent notes, and a
memory. Focus, Review, and Board remain contextual Home views, and Inbox remains
a contextual Library view. Their existing APIs, tasks, reminders, completion,
snooze, and triage behavior are preserved. The four Home contexts retain one
shared view switcher; Focus keeps its status filters within that context, while
Board presents the workflow as horizontally scrollable Kanban lanes.

## No-Provider Guarantee

Without any configured model provider, Arivu still saves and fetches content,
sanitizes archives, builds local summaries, indexes text, stores notes and
links, projects non-semantic graph relationships, generates deterministic
insights, handles feedback, reviews material, imports/exports data, and restores
backups. Missing embeddings simply omit semantic-similarity edges.
