# Design Context

`PRODUCT.md` is the authoritative product model and `DESIGN.md` is the
authoritative interface-system reference. This page is the short OpenWiki map
for agents working in the embedded frontend.

## Product Frame

Arivu is a private, self-hosted second brain organized around **Capture ->
Connect -> Discover -> Learn**. The interface must support immediate capture for
new users while exposing connections, graph exploration, and evidence-backed
learning patterns progressively.

The five primary destinations are Home, Library, Notes, Graph, and Insights. Capture
and Search / Ask stay globally available. Secondary workflows remain available
as contextual views, settings, or query-preserving compatibility routes.

## Visual Direction

Use the Brightlight-derived warm editorial system defined in `DESIGN.md`: a
white and pale-sand canvas, neutral ink, coral accent, local serif heading
stack, local sans-serif UI stack, fine dashed rules, compact radii, pill
controls, and restrained soft shadows. The application is deliberately
light-only and sets `color-scheme: light`; OS dark-mode preference must not
replace the palette.

This is a presentation layer, not a product or information-architecture
change. Preserve the existing routes, primary destinations, navigation, menu
layout and options, content structure, and functionality. The licensed
Brightlight project is a visual reference only. Do not copy or ship its Astro
runtime, Tailwind setup, JavaScript, font files, images, or other assets.

## Implementation Rules

- Keep `internal/app/web` dependency-free and native-browser-first.
- Implement the visual system with first-party HTML, CSS, SVG, and browser
  JavaScript; use local font stacks with platform fallbacks.
- Extend shared dialogs, menus, tabs, toasts, busy buttons, inline messages,
  empty states, and route cleanup instead of duplicating patterns.
- Preserve keyboard, touch, slow-network, offline, loading, empty, failure,
  narrow viewport, long-content, and reduced-motion behavior.
- Keep body and placeholder contrast at WCAG AA or better.
- Every visual graph must retain the equivalent accessible node list.
- Raw JSON is not a normal object creation or editing control.
