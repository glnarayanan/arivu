# Arivu Design System

## Direction

Arivu uses a **quiet cartographic workspace**: calm, precise, spatial, and
centered on knowledge rather than application chrome. The interface should feel
trustworthy during long reading and research sessions in ordinary indoor light.
Structure comes from alignment, whitespace, typography, and subtle tonal
layers. The graph is the only surface where spatial texture is functional.

The design supersedes the former warm-paper brutalist direction. Do not restore
decorative grids, condensed display faces, hard offset shadows, heavy borders,
or repeated card scaffolding.

## Principles

1. Knowledge first. Navigation and controls support the current thought.
2. Progressive depth. Common capture and retrieval are obvious; advanced graph
   and maintenance controls appear in context.
3. Evidence stays close. Relationships and insights show provenance,
   confidence, and source links before asking for trust.
4. Native and resilient. Prefer semantic HTML, platform controls, CSS, SVG, and
   existing dependency-free primitives.
5. Equivalent access. A visual graph always has an operable list alternative.

## Tokens

Tokens live in `internal/app/web/styles.css` and use OKLCH.

- **Surfaces:** true or cobalt-tinted neutral paper, panel, field, quiet,
  stronger, and sidebar layers.
- **Text:** graphite `--ink`, high-contrast `--muted`, and AA-compliant
  `--placeholder` roles.
- **Accent:** deep cobalt `--accent` / `--accent-2`, reserved for primary
  actions, focus, selection, and active navigation.
- **Semantic roles:** danger, success, attention, information, highlight, and
  modal scrim. Color never carries meaning alone.
- **Spacing:** a 4, 8, 12, 16, 24, 32, and 48px rhythm.
- **Shape:** 8px controls, 8-12px bounded surfaces, pills only for compact tags
  and state filters.
- **Elevation:** subtle short shadows only where layering matters. Borders and
  wide decorative shadows must not be combined.
- **Motion:** 150-250ms state transitions using `--ease-out-quart`.

Light and dark palettes follow `prefers-color-scheme`. Native browser zoom and
pinch zoom remain available, while the Graph also provides explicit Zoom out,
Reset view, and Zoom in controls above its pannable canvas.

## Typography

Use one readable native system sans stack for interface, content, controls, and
data. Product headings use a compact fixed scale, never oversized fluid display
type. Headings balance naturally, body prose stays around 65-75 characters, and
long saved content may use the existing reader measure.

## Application Shell

- **Desktop:** quiet left rail, central workspace, and contextual right
  inspector where the surface needs one.
- **Tablet:** compact rail; graph inspector moves below the canvas.
- **Mobile:** fixed four-item bottom navigation, persistent Capture and Search /
  Ask actions, and dialogs/inspectors that fit safe areas.

The primary navigation contains exactly Home, Library, Graph, and Insights.
Notes, imports/exports, settings, administration, and account actions live under
the profile or contextual controls. The existing command palette remains under
More and `Cmd/Ctrl+K`.

## Components And States

Use the shared primitives already in `app.js`: shell, dialog, destructive
confirmation, menu, tabs, toast, busy button, inline form message, empty state,
and route cleanup. Interactive components need default, hover, focus, active,
disabled, loading, success, and error treatment as applicable.

- Use skeleton-like structural placeholders for asynchronous content when a
  loading state is visible; do not center ornamental spinners.
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

At minimum, validate authenticated Home, Library, Search / Ask, Graph, Insights,
bookmark detail, note detail, Settings, and legacy route aliases at desktop,
tablet, and 390x844 mobile sizes. Check light/dark, keyboard-only operation,
reduced motion, empty/loading/error/offline states, long text, viewport overflow,
console errors, graph/list parity, and contrast. Keep screenshots as temporary
review artifacts unless a committed regression fixture needs them.
