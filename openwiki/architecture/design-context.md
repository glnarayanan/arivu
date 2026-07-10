# Design Context

This is the working design context for Arivu's embedded frontend. It is based on the current project docs and repository instructions, and should be refined when the creator gives more specific brand references.

## Users

Arivu is for people who self-host their own bookmarking and reading memory: technical readers, researchers, operators, and small teams who want durable control over saved web pages, summaries, search, and resurfacing. They use it while saving links, finding prior material, reviewing archived pages, and administering a small private instance.

## Brand Personality

The interface should feel opinionated, durable, and tactile. It should project confidence in local ownership and low-dependency software rather than glossy SaaS polish. Copy should stay factual, direct, and compact.

## Aesthetic Direction

The current frontend uses a warm-paper brutalist direction: ink-heavy borders, condensed display type, serif reading text, mono UI labels, and a small set of utility colors. Preserve that physical archive quality. Avoid purple gradients, glass panels, generic card grids, stock SaaS composition, and npm-heavy UI patterns.

The brutalist system should stay calm on dense app surfaces. Use hard borders and tactile shadows for orientation, but reserve the heaviest weight for primary work surfaces. Secondary tools should sit behind native disclosure panels instead of competing with capture, reader, Focus, or Review work.

## Quality Bar

Treat the embedded browser UI as flagship polish for a self-hosted app: small enough to remain dependency-free, but finished enough that interaction states, keyboard paths, empty states, loading feedback, and mobile layouts feel intentional.

## Design Principles

- Keep the shipped frontend dependency-free and native-browser-first.
- Use strong hierarchy, visible state, and direct labels over decorative complexity.
- Make controls reliable under keyboard, touch, slow network, long text, and narrow viewport conditions.
- Reinforce the warm archive aesthetic through tokens and layout rhythm, not one-off styling.
- Prefer compact, useful product copy over marketing language.
- Keep mobile navigation compact enough that the current work surface appears quickly; use horizontal overflow or grouped controls rather than tall stacked route menus.

## Interface System

The embedded frontend keeps its design system inside the dependency-free assets:

- `styles.css` defines semantic OKLCH color roles for action, information,
  success, danger, attention, neutral surfaces, and focus. Reuse these roles
  instead of adding route-specific colors.
- Shared dimensions cover border weight, native control height, reader width,
  and reading measure. Heavy borders and offset shadows belong to primary work
  surfaces; supporting panels remain flat.
- `app.js` owns shared native-browser patterns such as dialogs, menus, tabs,
  toasts, form feedback, busy buttons, and escaped empty states. Extend these
  primitives when a pattern repeats rather than copying route-specific markup.
- Color never carries state alone: semantic surfaces retain labels, roles, and
  visible control text, with contrast kept at WCAG AA or better.
