# Arivu Design System

## Direction

Arivu uses a **Brightlight-derived warm editorial** visual language. The
reference theme supplies the look and feel only: a white and warm-sand canvas,
coral accents, neutral ink, serif editorial headings, restrained sans-serif
interface text, dashed rules, compact radii, pill controls, and sparse soft
shadows. It should feel bright, composed, and inviting during long reading and
research sessions.

Arivu is light-only. `color-scheme: light` is an intentional product decision;
do not add a dark palette or follow `prefers-color-scheme` for colors.

This overhaul does not redefine the product. Routes, navigation destinations,
menu layout and options, content hierarchy, interactions, and functionality
remain unchanged. When applying the system to another surface, restyle the
existing markup and behavior rather than reorganizing it.

Brightlight is a licensed design reference, not a runtime dependency. Its
Astro components, Tailwind setup, scripts, font files, and image assets are not
copied into the shipped application. Arivu independently self-hosts the
OFL-licensed Geist and Noto Serif webfonts used by the visual language and
remains a dependency-free embedded frontend implemented with first-party HTML,
CSS, SVG, and browser JavaScript.

## Principles

1. Editorial warmth. Use warm canvas tones and confident type hierarchy rather
   than application chrome to make saved knowledge pleasant to revisit.
2. Familiar structure. Preserve current navigation, menus, routes, information
   architecture, and behavior while changing presentation.
3. Coral with restraint. Reserve the accent for primary actions, selection,
   focus, and small moments of emphasis.
4. Native and resilient. Prefer semantic HTML, platform controls, CSS, SVG, and
   existing dependency-free primitives.
5. Equivalent access. A visual graph always has an operable list alternative.

## Tokens

Tokens live in `internal/app/web/styles.css` and use OKLCH.

- **Canvas:** warm sand `--paper` surrounds white `--panel` and `--sidebar`
  surfaces. Quiet surfaces use pale sand, not gray-blue.
- **Text:** near-black base neutrals provide `--ink`; muted and placeholder
  roles remain dark enough for WCAG AA.
- **Accent:** the coral family `--accent-50` through `--accent-800` supplies
  primary actions, links, focus, selection, and active navigation.
- **Semantic roles:** danger, success, information, highlight, and modal scrim
  remain distinct. Color never carries meaning alone.
- **Spacing:** use the established 4, 8, 12, 16, 24, 32, 48, and 64px rhythm.
- **Shape:** compact 6-12px radii contain fields and surfaces. Pills are for
  buttons, navigation states, tags, and compact filters.
- **Rules:** use fine dashed neutral separators to establish editorial rhythm.
  Avoid heavy borders and decorative grids.
- **Elevation:** use `--shadow-sm` and `--shadow` sparingly for true layering,
  such as primary work surfaces, dialogs, menus, and toasts.
- **Motion:** retain short 150-250ms state transitions using
  `--ease-out-quart`; honor reduced-motion preferences.

Native browser and pinch zoom remain available. Graph also provides explicit
Zoom out, Reset view, and Zoom in controls above its pannable canvas.

## Typography

Use `--font-display`, a locally available serif stack led by Noto Serif, for
brand marks, page titles, panel headings, and other editorial moments. Use
`--font-body` / `--font-ui`, a locally available sans-serif stack led by Geist,
for prose, labels, controls, and data. `--font-mono` is limited to technical
values that benefit from fixed-width alignment.

The official OFL-licensed Geist, Geist Mono, and Noto Serif webfonts are bundled
under `internal/app/web/fonts`, served first-party, and covered by local license
notices. No font is fetched from the Brightlight checkout or a third-party CDN,
and every stack retains durable platform fallbacks. Keep headings compact enough for product
work, body prose near 65-75 characters, and saved reading content within the
existing reader measure.

## Application Shell

The existing shell is intentionally unchanged:

- **Desktop:** left rail, central workspace, and contextual right inspector
  where the surface already needs one.
- **Tablet:** compact rail; graph inspector moves below the canvas.
- **Mobile:** fixed five-item bottom navigation, persistent Capture and Search /
  Ask actions, and dialogs/inspectors that fit safe areas.

Primary navigation remains exactly Home, Library, Notes, Graph, and Insights.
Imports/exports, settings, administration, and account actions remain under the
profile or contextual controls. The command palette remains under More and
`Cmd/Ctrl+K`.

## Components And States

Restyle the shared primitives already in `app.js`: shell, dialog, destructive
confirmation, menu, tabs, toast, busy button, inline form message, empty state,
and route cleanup. Do not replace their behavior or create theme-only variants.
Interactive components need default, hover, focus, active, disabled, loading,
success, and error treatment as applicable.

- Primary actions use coral pills; quieter actions use sand or white surfaces.
- Fields and structured content use compact radii and clear neutral rules.
- Dashed separators divide editorial sections without wrapping every item in a
  card.
- Use structural placeholders for visible asynchronous loading; do not center
  ornamental spinners.
- Empty states explain the next useful action.
- Destructive actions name the outcome and require confirmation.
- Forms retain labels, inline errors, `aria-describedby`, native validity, and
  specific busy labels.
- Object creation uses type-specific native fields. Raw JSON is not a normal
  creation or editing control.

## Graph Semantics

Node color is a secondary cue; labels and accessible type names remain present.
Current semantic roles cover bookmarks, notes, daily notes, annotations,
knowledge objects, entities, and concepts. Solid edges indicate explicit
relationships; derived relationships use a quieter or dashed treatment.

The graph is intentionally bounded. The current browser requests at most 48
nodes and 160 edges, while the API enforces its own higher bounds. A focus
selector requests local expansion without first rendering a global hairball.
The SVG supports keyboard selection and an inspector. The always-visible
`Accessible node list` exposes the same nodes without requiring SVG perception.
The canvas is scrollable on constrained screens, and normal browser/touch zoom
remains available.

## Accessibility

- Target WCAG AA contrast: 4.5:1 for body and placeholder text; 3:1 for large
  text, focus indicators, and meaningful graphical controls.
- Preserve skip links, landmarks, heading order, route announcements, document
  titles, and focus restoration after navigation.
- Keep visible `:focus-visible` treatment on links, controls, SVG nodes, menus,
  dialogs, and graph-list buttons.
- Dialogs trap focus, close with Escape, restore focus, and use accessible names.
- Menus and tabs retain their existing ARIA and keyboard behavior.
- Never require color, hover, pointer precision, animation, or a canvas-only
  representation to complete a task.

## Motion

Motion communicates routing, state changes, menu/dialog appearance, and
feedback only. Do not animate layout properties or stage page-load sequences.
Every transition must have an effective `prefers-reduced-motion: reduce`
alternative; content must be visible before animation.

## Quality Checks

At minimum, validate authenticated Home, Library, Notes, Search / Ask, Graph, Insights,
bookmark detail, note detail, Settings, and legacy route aliases at desktop,
tablet, and 390x844 mobile sizes. Validate the light-only palette even when the
operating system requests dark appearance. Check keyboard-only operation,
reduced motion, empty/loading/error/offline states, long text, viewport overflow,
console errors, graph/list parity, and contrast. Keep screenshots as temporary
review artifacts unless a committed regression fixture needs them.
