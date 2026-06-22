# Interface Quality Audit

Date: 2026-06-22

Scope: embedded browser UI under `internal/app/web`, with source review, contrast calculation, and browser checks against a local Go server.

## Anti-Patterns Verdict

Pass. The interface does not read as generic AI-generated SaaS output. It has a clear warm-paper brutalist direction, distinctive typography, strong ink borders, direct product copy, and no purple gradients, glass cards, gradient text, stock hero metrics, generic icon cards, or glowing dark-mode defaults.

The main design risk is not visual slop. It is incomplete auth-adjacent flows: the UI presents routes for password reset and invite acceptance, but unauthenticated users are redirected back to sign-in.

## Executive Summary

Issues found:

- Critical: 0
- High: 2
- Medium: 4
- Low: 3

Overall quality score: 82/100.

Most important fixes:

1. Make `/reset-password` usable while signed out and connect it to the existing reset API.
2. Make `/accept-invite` usable while signed out, or remove the route until the invite flow exists.
3. Add a discoverable "Forgot password" entrypoint from `/auth`.
4. Improve error status semantics and inline field-level feedback.
5. Normalize the color token layer if future theming is expected.

## Audit Evidence

Commands and checks run:

- `node --check internal/app/web/app.js`
- Static scans for console/debug leftovers, hard-coded colors, dimensions, ARIA, roles, scripts, external assets, and motion properties.
- Contrast ratios calculated for key token pairs:
  - `ink` on `paper`: 16.17:1
  - `muted` on `panel`: 6.22:1
  - `placeholder` on `field`: 7.63:1
  - `accent-ink` on `accent`: 5.56:1
  - `accent-ink` on `accent-2`: 5.66:1
  - `ink` on `sidebar`: 12.51:1
- Browser checks with:

```bash
GOCACHE=/private/tmp/arivu-build-cache \
ARIVU_DB=/private/tmp/arivu-audit.sqlite3 \
SECRET_KEY=audit-browser-secret-key-32-bytes \
SIGNUPS_ENABLED=true \
go run ./cmd/arivu serve -addr 127.0.0.1:18080
```

Rendered checks covered `/auth`, signup to `/dashboard`, Actions menu keyboard movement, `/settings` tabs, 390x844 mobile layout, and temporary 200% root text scaling.

## Detailed Findings

### Critical Issues

None found.

### High-Severity Issues

#### H1. Password reset route is unreachable for signed-out users

- Location: `internal/app/web/app.js:20`, `internal/app/web/app.js:480-483`, `internal/app/app.go:78-79`, `internal/auth/auth.go:370-376`
- Category: Accessibility / UX / Core workflow
- Description: `/reset-password` is registered in the frontend route table, and the backend exposes forgot/reset password APIs plus email links to `/reset-password?token=...`. The frontend route uses `simplePage`, which calls `requireUser()`. In an isolated signed-out browser session, opening `/reset-password` redirected to `/auth` and logged a 401 from `/api/auth/me`.
- Impact: Users who receive a password-reset link cannot complete account recovery from the browser UI. This blocks a core auth recovery path.
- Standard: WCAG 3.3.3 Help, WCAG 3.2.3 Consistent Navigation, functional auth requirement.
- Recommendation: Implement a public reset page that reads the token from the URL, collects a new password, submits `POST /api/auth/reset-password`, and shows inline success/error states. Do not call `requireUser()` for this route.
- Suggested command: `/harden`

#### H2. Invite acceptance route is unreachable for signed-out users

- Location: `internal/app/web/app.js:21`, `internal/app/web/app.js:480-483`, `internal/app/admin.go:84-100`, `internal/app/admin.go:121-137`
- Category: Accessibility / UX / Onboarding
- Description: `/accept-invite` is also registered through `simplePage`, so signed-out invitees are redirected to `/auth`. The backend has admin invite state and admin password reset behavior, but the embedded frontend has no public invite acceptance UI.
- Impact: Invited users cannot onboard through the advertised route. This creates a dead-end first-run experience for non-admin users.
- Standard: WCAG 3.3.2 Labels or Instructions by analogy for onboarding instructions, functional onboarding requirement.
- Recommendation: Either implement a public accept-invite flow with token/password setup, or remove the route until the product has a concrete invite acceptance contract. If invites are intentionally admin-mediated, make the admin UI copy reflect that.
- Suggested command: `/onboard`

### Medium-Severity Issues

#### M1. Auth page has no forgot-password entrypoint

- Location: `internal/app/web/app.js:325-369`, `internal/app/app.go:78-79`
- Category: UX / Accessibility
- Description: The backend exposes `POST /api/auth/forgot-password`, but the auth screen only offers Sign in and Create account. There is no visible path for users who forgot their password.
- Impact: Users cannot discover account recovery without knowing API routes or receiving an external reset email through another channel.
- Standard: WCAG 3.3.3 Help.
- Recommendation: Add a "Forgot password" action on `/auth` that reveals or navigates to a public email form, submits `POST /api/auth/forgot-password`, and returns the backend's account-enumeration-safe success message.
- Suggested command: `/onboard`

#### M2. Error toasts use polite status semantics

- Location: `internal/app/web/index.html:13`, `internal/app/web/app.js:73-81`
- Category: Accessibility
- Description: All toast messages are appended to a single `aria-live="polite"` region and each toast receives `role="status"`, including error messages.
- Impact: Screen reader users may hear critical failures late or miss them during continued interaction. Errors generally need assertive announcement or inline association with the field/action that failed.
- Standard: WCAG 4.1.3 Status Messages.
- Recommendation: Keep success toasts polite, but use `role="alert"` or an assertive region for errors. Pair form errors with inline text and `aria-describedby` where possible.
- Suggested command: `/harden`

#### M3. Form errors are not associated with fields

- Location: `internal/app/web/app.js:331-336`, `internal/app/web/app.js:377-384`
- Category: Accessibility / UX Writing
- Description: Login, signup, save URL, and search use visible labels, but API failures only appear as global toast text. The fields have no persistent error container or `aria-describedby` link to error text.
- Impact: Keyboard and screen reader users may not know which field caused the problem, especially after failed login/signup or failed bookmark creation.
- Standard: WCAG 3.3.1 Error Identification, WCAG 3.3.3 Help.
- Recommendation: Add reusable field error slots for auth and save URL flows. On failed submit, set text near the affected field or form, link it with `aria-describedby`, and keep the toast as secondary feedback.
- Suggested command: `/clarify`

#### M4. Protected-route redirects create console errors

- Location: `internal/app/web/app.js:487-495`
- Category: Quality / Debuggability
- Description: Signed-out access to public-looking auth routes triggered `GET /api/auth/me` and a browser console error: `401 (Unauthorized)`. The console was clean for normal auth/dashboard/settings flows, but not for these route-guarded flows.
- Impact: Console errors make smoke checks noisy and can hide real frontend regressions. They also indicate the UI is attempting a protected fetch before deciding whether the route is public.
- Standard: Project browser workflow standard: console warning/error collection should stay empty during completed checks.
- Recommendation: Classify public, protected, and auth-only routes before calling `requireUser()`. Avoid probing `/api/auth/me` for known public routes.
- Suggested command: `/harden`

### Low-Severity Issues

#### L1. Color tokens are not yet a semantic OKLCH palette

- Location: `internal/app/web/styles.css:1-17`, `internal/app/web/index.html:6`
- Category: Theming
- Description: The current colors are centralized as CSS variables, but they are still mostly hex/RGB tokens and include a hard-coded `theme-color` meta value. The frontend-design guidance prefers perceptual OKLCH values and semantic token layers.
- Impact: Current contrast is good, but future color changes or dark-mode work will be more error-prone because token roles and color derivations are not explicit.
- Standard: Project design guidance, not a WCAG violation.
- Recommendation: Introduce semantic tokens such as `--color-bg`, `--color-surface`, `--color-text`, `--color-action`, and `--color-danger`, with OKLCH primitives under them. Keep the existing visual direction.
- Suggested command: `/normalize`

#### L2. Hover transitions include `box-shadow`

- Location: `internal/app/web/styles.css:459-472`
- Category: Performance / Motion
- Description: The transition list includes `box-shadow` for buttons, links, inputs, and bookmark cards. The motion guidance prefers transform and opacity because shadow animation causes repaints.
- Impact: This is unlikely to matter on the current small screens, but it can become janky on low-power devices or dense bookmark grids.
- Standard: Motion performance best practice.
- Recommendation: Keep the tactile pressed/lifted effect but limit animation to `transform` and color where possible, or reduce shadow changes for repeated grid items.
- Suggested command: `/optimize`

#### L3. `overflow-x: hidden` can mask future layout defects

- Location: `internal/app/web/styles.css:57`
- Category: Responsive / Hardening
- Description: Browser checks at 390px and 200% root text scaling did not find real horizontal overflow, but global `overflow-x: hidden` can hide future clipped content instead of exposing the failure during QA.
- Impact: If a later feature introduces a too-wide table, URL, code block, or action row, users may lose access to clipped content without a horizontal escape path.
- Standard: Responsive resilience best practice.
- Recommendation: Keep overflow control on known risky components instead of the whole body, or add explicit regression checks for offscreen elements when this rule remains.
- Suggested command: `/harden`

## Patterns & Systemic Issues

- Auth-adjacent route classification is the main systemic gap. Public recovery and invite routes are treated like protected pages.
- Error feedback is global-first. Toasts are useful, but field-level errors and assertive error announcements are not yet in place.
- The design system is centralized but shallow: colors and spacing are tokenized, while semantic token roles and theming layers are still minimal.

## Positive Findings

- Anti-pattern pass is strong: no glassmorphism, purple gradients, gradient text, hero metric template, stock icon-card grid, or generic SaaS composition.
- Primary text and control contrast pass WCAG AA; most measured pairs are comfortably above 5.5:1.
- `/auth`, `/dashboard`, and `/settings` expose clear accessible names in browser snapshots.
- Skip link exists and receives a visible 3px focus ring when reached by keyboard.
- Actions menu uses `aria-haspopup`, `aria-expanded`, `role="menu"`, `role="menuitem"`, Escape close, and roving focus.
- Settings tabs use `role="tablist"`, `role="tab"`, `role="tabpanel"`, `aria-selected`, hidden inactive panels, and ArrowRight movement.
- Mobile 390x844 checks found no horizontal overflow, no offscreen elements, and no measured controls below 44x44.
- Reduced-motion handling exists and route progress uses transform rather than width animation.
- No npm dependency tree or external frontend asset pipeline was introduced.

## Recommendations by Priority

Immediate:

1. Fix `/reset-password` as a public route and wire it to the reset API.
2. Decide and implement the invite acceptance model, or remove the route from the frontend until it exists.

Short-term:

1. Add a forgot-password entrypoint on `/auth`.
2. Add assertive error semantics and field-level error messages for auth and save forms.
3. Prevent expected signed-out route handling from producing browser console errors.

Medium-term:

1. Normalize color tokens into a semantic palette while preserving the current aesthetic.
2. Reduce repaint-heavy shadow transitions for repeated interactive elements.

Long-term:

1. Add automated browser workflow coverage for public auth recovery, invite acceptance, and 200% text scaling.
2. Revisit dark mode only if it becomes a product requirement; do not add it just to satisfy theming completeness.

## Suggested Commands for Fixes

- `/harden`: Address public/protected route classification, password reset, console noise, assertive error announcements, and field-level error resilience.
- `/onboard`: Design and implement the invite acceptance and forgot-password entrypoints.
- `/clarify`: Improve form error copy, recovery copy, and field-associated messages.
- `/normalize`: Convert current color variables into a semantic token system without changing the warm-paper brutalist direction.
- `/optimize`: Remove repaint-heavy motion from repeated controls/cards and add lightweight regression checks.
