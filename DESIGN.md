# Arivu Design System

## Direction

Arivu follows the Lexington Themes **Spaziobianco** design language, applied to
a working application: warm paper, a serif display face at regular weight,
borderless muted cards with soft shadows, charcoal buttons with layered edges,
and hairlines only where a divider is needed. Coral stands in for the theme's
orchid, pink, and periwinkle accents and remains Arivu's accent. It should feel
calm, composed, and unhurried during long reading and research sessions.

Arivu is light-only. `color-scheme: light` is an intentional product decision;
do not add a dark palette or follow `prefers-color-scheme` for colors. The one
dark moment is the dithered art panel on the auth screens, which is an image,
not a theme.

The knowledge-focused product pass changes navigation and retires planning
workflows as described in `PRODUCT.md`. It retains this visual system and the
existing shared interaction primitives.

The implementation is first-party HTML, CSS, SVG, and browser JavaScript in a
dependency-free embedded frontend. Values below come from the theme source.
When changing the app, check the theme source first and keep these values
unless a deviation is listed here.

## Principles

1. Editorial warmth. Warm paper tones and a confident serif hierarchy make
   saved knowledge pleasant to revisit.
2. Familiar structure. Preserve current navigation, menus, routes, information
   architecture, and behavior while changing presentation.
3. Coral with restraint. Reserve the accent for selection, focus, links in
   emphasis, and small moments of color. Primary actions are charcoal.
4. Soft depth, no outlines. Cards are muted fills with a shadow, not boxed.
5. Native and resilient. Prefer semantic HTML, platform controls, CSS, SVG, and
   existing dependency-free primitives.
6. Equivalent access. A visual graph always has an operable list alternative.

## Tokens

Tokens live in `internal/app/web/styles.css` and use OKLCH.

- **Paper and fill:** `--paper` is `oklch(98% 0.005 80.72)`. `--fill` (the
  theme's muted) is `oklch(94% 0.008 81.79)`, with `--fill-deep` one step
  darker for hover and disabled states. Pages sit on paper; cards sit on fill.
- **Ink:** `--ink` is `oklch(26.45% 0 89.88)`. `--ink-strong` is used for
  headings and active navigation.
- **Secondary text:** the theme's muted foreground, `oklch(55.1% 0.012 76.53)`,
  fails AA on fill cards. `--text-2` is the same hue at 47% lightness so
  secondary copy passes 4.5:1 on paper and on fill. Placeholder text uses
  `--base-600`.
- **Hairlines:** `--hairline` (also `--line`) is the only divider color.
- **Accent:** a coral ramp, `--accent-50` to `--accent-800`. `--accent` is
  `--accent-700` for text and links; `--accent-600` drives the focus ring and
  the active-navigation bar. `--highlight` is `--accent-200`. The legacy
  `--sand-*` and `--base-*` names are aliases onto the warm paper family so
  older rules recolor automatically.
- **Selection:** `::selection` is charcoal with `--accent-300` text.
- **Shadows:** `--shadow`, `--shadow-lg`, `--shadow-xl`, and the five-layer
  `--shadow-2xl` follow the theme. Buttons use `--shadow-button-default` and
  `--shadow-button-muted`.
- **Spacing:** use the established 4, 8, 12, 16, 24, 32, 48, and 64px rhythm.
- **Shape and concentric radii:** cards use `--radius-xl` (16px, `rounded-2xl`).
  Content nested inside a card with 8px padding uses 12px radii; fields and
  small controls use 8px. Pills are only for chips and compact filters.
- **Motion:** retain short 150-250ms state transitions using
  `--ease-out-quart`; honor reduced-motion preferences.

Native browser and pinch zoom remain available. Graph also provides explicit
Zoom out, Reset view, and Zoom in controls above its pannable canvas.

## Typography

- `--font-display`: **STIX Two Text** at weight 400 with negative tracking
  (`-0.025em`) for the brand, page titles, `h2` headings, panel headings, and
  dialog titles. Titles are 1.875rem, 2.25rem from 1024px; panel headings
  1.5rem; sub-headings 1.25rem.
- `--font-body` / `--font-ui`: **Geist** for navigation, controls, prose,
  labels, and data. Geist is the one deliberate deviation from the theme,
  whose sans is not bundled.
- `--font-mono`: **Geist Mono**, limited to technical values.

All faces are bundled under `internal/app/web/fonts`, served first-party, and
covered by local license notices. Nothing is fetched from a CDN, and every
stack retains durable platform fallbacks. Keep body prose near 65-75
characters and saved reading content within the existing reader measure.
There are no uppercase kickers or letter-spaced labels.

## Application Shell

- **Desktop:** a solid paper left rail separated by a hairline, a central
  workspace, and a contextual right inspector where a surface needs one. The
  active navigation item shows a 2px coral bar on its left edge and
  `--ink-strong` text; there is no pill fill.
- **Tablet:** compact rail; graph inspector moves below the canvas.
- **Mobile:** fixed four-item bottom navigation, persistent Capture and Search
  actions, and dialogs/inspectors that fit safe areas.

Primary navigation is Home, Library, Notes, and Search. Graph and Review are
secondary destinations under More. There is no standalone Insights page.
Imports/exports, settings, administration, and account actions remain under the
profile or contextual controls. The command palette remains under More and
`Cmd/Ctrl+K`.

The topbar is solid paper with a hairline underneath, not translucent.

## Components And States

Restyle the shared primitives already in `app.js`: shell, dialog, destructive
confirmation, menu, tabs, toast, busy button, inline form message, empty state,
and route cleanup. Do not replace their behavior or create theme-only variants.
Interactive components need default, hover, focus, active, disabled, loading,
success, and error treatment as applicable.

- **Buttons:** the default is a charcoal gradient from `--ink` at 85% to
  `--ink` with the layered `--shadow-button-default`. Hover raises
  `brightness` to 1.25, active scales to 0.98, and focus shows a 3px
  `--accent` ring at 40% plus a 2px outline so the indicator survives forced
  colors. Muted (secondary and icon) buttons run from `--paper` to `--fill`
  with `--shadow-button-muted` and hover at brightness 0.97. Danger uses the
  same construction in `--danger`. Disabled controls use `--fill-deep` with
  `--text-2` text, no shadow, and no motion.
- **Sizes:** extra small `h-8 px-3 text-xs rounded-md`, small
  `h-9 px-4 text-sm rounded-md`, base `h-10 px-4 text-base rounded-lg`.
  Desktop controls may be small; mobile and touch targets stay 44px.
- **Cards:** `rounded-2xl` on `--fill` with `--shadow` and no outline. The
  primary reading surface is paper with `--shadow-lg` and a hairline so it
  separates from the page. Inputs inside a card sit on paper.
- **Fields:** `--fill` background, 8px radius, a transparent border that shows
  `--fill-deep` on hover, and a paper background on focus. Keep 1rem type in
  operate forms.
- **Dividers:** hairlines only, and only where the structure needs one. Do not
  wrap every item in a card or draw dashed rails.
- Use structural placeholders for visible asynchronous loading; do not center
  ornamental spinners.
- Empty states explain the next useful action.
- Destructive actions name the outcome and require confirmation.
- Forms retain labels, inline errors, `aria-describedby`, native validity, and
  specific busy labels.

## Auth And Public Share

The auth screens use the theme's split layout: a form column (24rem maximum)
beside a rounded dark art panel from 1024px up. Below that width only the form
shows. The panel is `internal/app/web/art/auth-dither.webp`, a static image
that is decorative (`aria-hidden`). Never put text on it.

The art is generated by `scripts/generate-dither-art.py`, a deterministic
numpy port of the theme's shader: warped fractional Brownian noise cut into
four flat colors with an 8x8 Bayer threshold. Edit the script and rerun it
instead of editing the WebP. The theme's live WebGL shader is not reproduced.

The public share page (`public-share.css`) is self-contained and uses the same
paper, ink, STIX Two Text, and hairline values.

## Graph Semantics

Node color is a secondary cue; labels and accessible type names remain present.
Current semantic roles cover bookmarks, notes, annotations, entities, and
concepts. Solid edges indicate explicit relationships; derived relationships
use a quieter or dashed treatment. Dashed strokes are reserved for this
meaning and are never used as decoration.

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

At minimum, validate authenticated Home, Library, Notes, Search, Graph, Review,
bookmark detail, note detail, Settings, the auth screen, and legacy route
aliases at desktop (1440), tablet (about 820), and 390x844 mobile sizes.
Validate the light-only palette even when the operating system requests dark
appearance. Check keyboard-only operation, reduced motion,
empty/loading/error/offline states, long text, viewport overflow, console
errors, graph/list parity, and contrast. Keep screenshots as temporary review
artifacts unless a committed regression fixture needs them.
