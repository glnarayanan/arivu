# Design Context

`PRODUCT.md` is the authoritative product model and `DESIGN.md` is the
authoritative interface-system reference. This page is the short OpenWiki map
for agents working in the embedded frontend.

## Product Frame

Arivu is a private, self-hosted second brain organized around **Capture ->
Connect -> Discover -> Learn**. The interface must support immediate capture for
new users while exposing connections, graph exploration, and evidence-backed
learning patterns progressively.

The four primary destinations are Home, Library, Graph, and Insights. Capture
and Search / Ask stay globally available. Secondary workflows remain available
as contextual views, settings, or query-preserving compatibility routes.

## Visual Direction

Use a quiet cartographic workspace: true or cobalt-tinted neutrals, graphite
text, one deep cobalt accent, restrained semantic colors, one native sans stack,
subtle tonal layers, and functional spatial texture only in the graph. Light
and dark palettes follow the system theme.

The former warm-paper brutalist direction is obsolete. Do not reintroduce hard
offset shadows, decorative grids, condensed display typography, heavy borders,
or repeated card scaffolding.

## Implementation Rules

- Keep `internal/app/web` dependency-free and native-browser-first.
- Extend shared dialogs, menus, tabs, toasts, busy buttons, inline messages,
  empty states, and route cleanup instead of duplicating patterns.
- Preserve keyboard, touch, slow-network, offline, loading, empty, failure,
  narrow viewport, long-content, and reduced-motion behavior.
- Keep body and placeholder contrast at WCAG AA or better.
- Every visual graph must retain the equivalent accessible node list.
- Raw JSON is not a normal object creation or editing control.
