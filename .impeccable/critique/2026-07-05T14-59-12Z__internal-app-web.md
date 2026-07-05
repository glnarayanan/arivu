---
target: internal/app/web
total_score: 24
p0_count: 0
p1_count: 2
timestamp: 2026-07-05T14-59-12Z
slug: internal-app-web
---
Method: dual-agent (A: 019f32c4-9f36-7140-842c-db61a78f7b56 · B: 019f32c4-be84-7170-ab6c-8786473d030f)

**Design Health Score**

| # | Heuristic | Score | Key Issue |
|---|-----------|---:|-----------|
| 1 | Visibility of System Status | 3 | Busy buttons, route progress, toasts, and save/job status exist; enrichment failure and retry states are still indirect. |
| 2 | Match System / Real World | 3 | The product language is mostly direct, but processed/resurfacing/enrichment/graph still assume product literacy. |
| 3 | User Control and Freedom | 3 | Destructive confirmations and cancel paths are solid; undo is missing after archive/complete/delete-like actions. |
| 4 | Consistency and Standards | 3 | Native controls and tokens are consistent; the same heavy panel vocabulary flattens meaning across route types. |
| 5 | Error Prevention | 2 | Native validation and delete confirms help; dense forms and manual JSON still invite mistakes. |
| 6 | Recognition Rather Than Recall | 2 | Main actions are visible, but shortcut discovery, workflow order, and route purpose require memory. |
| 7 | Flexibility and Efficiency | 2 | Inbox has bulk and keyboard triage; broader power-user accelerators are sparse or hidden. |
| 8 | Aesthetic and Minimalist Design | 2 | Distinctive and coherent, but not minimal at task level; detail pages expose too much at once. |
| 9 | Error Recovery | 2 | Inline messages and toasts are clear; recovery often stops at logs or route-hunting. |
| 10 | Help and Documentation | 2 | Empty states teach lightly; dense workflow decisions lack contextual help. |
| **Total** | | **24/40** | **Acceptable: strong identity, significant workflow simplification needed.** |

**Anti-Patterns Verdict**

This does not read like generic AI SaaS. It has a real design point of view: warm paper, ink-heavy borders, condensed display type, serif reading text, mono UI labels, native controls, no purple gradients, no glass panels, no stock SaaS shine.

The risk is not AI slop. The risk is an overloaded brutalist admin console wearing a strong archive skin. The identity is good, but repeated panels, hard borders, shadows, uppercase headings, and equal ink weight make too many surfaces compete.

Deterministic detector result: clean. `detect.mjs --json internal/app/web` returned `[]` with 0 findings. This agrees with the LLM assessment on AI-slop avoidance, but the detector does not measure product workflow overload, mobile nav dominance, or visual hierarchy flattening.

Browser overlays were not available for this run because fresh-tab browser automation failed before page navigation. No user-visible overlay should be claimed.

**Overall Impression**

Arivu has escaped the generic AI-app trap, but it has not yet escaped feature-surface overload. The strongest screen is Inbox because it has a clear job. Dashboard and bookmark detail need that same ruthlessness: fewer simultaneous modes, clearer primary action, and progressive disclosure for advanced second-brain operations.

**What's Working**

- The visual direction is specific and defensible. The archive-paper brutalism feels self-hosted, tactile, and durable instead of glossy SaaS.
- Accessibility basics are better than average: skip link, focus states, route announcements, native controls, tab roles, destructive confirmations, and labeled filters are now present.
- The product loop is real. Capture, Inbox, Focus, Review, notes, reminders, links, and resurfacing exist as working UI, not only roadmap language.

**Priority Issues**

**P1 - The primary loop is visually unfocused**

What: Dashboard exposes capture, search, filters, answer, saved-search creation, saved searches, and results together.

Why it matters: First-timers and returning users must choose the mode before doing the task. A second-brain app should make capture and recall feel immediate.

Fix: Make capture/search/results the core dashboard. Move saved-search creation and advanced filters behind progressive controls.

Suggested command: `$impeccable distill internal/app/web dashboard flow`

**P1 - Bookmark detail is an all-at-once workbench**

What: Bookmark detail exposes reading, processing, annotations, linked notes, explicit links, action items, reminders, related items, and review actions in one long cockpit.

Why it matters: This is the core saved-page-to-knowledge surface, but it feels like maintenance before reading.

Fix: Lead with reader plus processing state. Collapse annotations, links, tasks, reminders, and related items into intent-triggered sections or route-local tabs.

Suggested command: `$impeccable shape internal/app/web bookmark detail`

**P2 - Mobile navigation buries the product**

What: At phone width, the full navigation stack appears before the work surface.

Why it matters: Mobile opens as a menu, not a tool. The capture/search task starts too low for one-handed use.

Fix: Use compact mobile navigation: current route plus menu, grouped overflow, or a bottom rail for the few daily routes.

Suggested command: `$impeccable adapt internal/app/web shell navigation`

**P2 - The visual system has too few weight classes**

What: Ink borders, shadows, panels, uppercase headings, and mono labels are applied broadly.

Why it matters: The aesthetic is good, but primary, secondary, and background information compete.

Fix: Keep the brutalist identity, but introduce hierarchy: lighter secondary panels, fewer shadows, quieter metadata, stronger primary work areas.

Suggested command: `$impeccable layout internal/app/web styles.css`

**P3 - Recovery paths are too operator-coded**

What: Failed processing can resolve to log-oriented guidance, and enrichment recovery is not actionable enough where the failure appears.

Why it matters: Technical users can read logs, but product UI should offer recovery at the point of failure.

Fix: Show failed enrichment state on bookmark cards/detail with retry, dismiss, and job detail affordances.

Suggested command: `$impeccable harden internal/app/web job and enrichment states`

**Persona Red Flags**

**Alex, power user:** Inbox has keyboard triage, but shortcuts are invisible. Bookmark detail has too many one-by-one controls. Global Actions repeats navigation rather than accelerating real work.

**Jordan, first-timer:** Dashboard does not clearly say "save first, then triage in Inbox" at the action level. Terms like enrichment, processing, processed, resurfacing, and graph need more context. After save, the next best step is weak.

**Sam, accessibility-dependent user:** The baseline is good, but repeated generic buttons like Done/Delete/Archive still depend heavily on surrounding context. Toasts disappear after 3.2 seconds, which can be rough for screen-reader or cognitive-access users.

**Casey, distracted mobile user:** Mobile first viewport is dominated by navigation. Primary dashboard action appears too low. Workflows require too much typing and scrolling despite acceptable tap target sizes.

**Minor Observations**

- The font stack is distinctive, but fallback to Impact could cheapen the tone on systems without the preferred fonts.
- Saved search creation appears too early for first-run or small-library users.
- The repeated card/panel grid is not visually generic, but structurally it still behaves like a card-heavy dashboard.
- Inbox is the clearest screen and should influence the rest of the hierarchy.
- Assistant manual JSON is correctly hidden in `details`; that disclosure pattern should be reused for other advanced controls.

**Questions to Consider**

- What is the one thing a user should do within 30 seconds after saving a page?
- Should Saved Knowledge be a capture surface, a search surface, or a daily command center?
- What if bookmark detail had one primary next action instead of exposing every possible knowledge operation?
- Are Graph, Analytics, Duplicates, Admin, and Settings all primary navigation for the same user every day?
- Could Arivu feel more powerful by showing less at first?
