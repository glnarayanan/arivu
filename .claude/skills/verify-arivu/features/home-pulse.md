# Home pulse

Home is the signed-in landing surface: daily note, new captures, review candidates, recent notes, and Fast capture links.

## Sub-features

- `home-open` shows Pulse after signup or via Primary `Home`.
- `home-views` switches Pulse, Focus, Review, and Board without leaving Home.
- `daily-note-save` persists today's daily note.
- `home-recent-notes` lists a previously captured standalone note.

## How to get to it (user POV)

- After `Create account` or `Sign in`, the app navigates to `/today`.
- Choose `Home` in the Primary nav, or the brand `Arivu home`.
- Compatibility: `/focus`, `/review`, `/board` open Home views.

## Driving it with control-arivu

Preconditions:

- Isolated instance is healthy.
- Verify account exists. For `home-recent-notes`, [Capture a note](./capture-note.md) has already saved `Release checklist`.

- **Land on Home.** Open `/today`. `#route-title` reads `Home`. `nav` named `Home views` includes Pulse, Focus, Review, Board. Heading `Daily note` is visible. Fast capture includes links `Capture`, `New note`, `Ask Arivu`.
- **Save daily note.** Fill `Plan, decisions, loose thoughts` with `Ship the verification skill`. Choose `Save daily note`. Reload `/today`. The textarea still contains `Ship the verification skill`.
- **Recent notes.** Heading `Recent notes` includes `Release checklist` (or an `Open note` link to it).
- **View tabs.** Choose `Focus`. URL is `/today?view=focus`. Choose `Review`. URL is `/today?view=review`. Choose `Board`. URL is `/today?view=board` and region `Knowledge workflow board` is present. Return to Pulse via `Pulse` or `/today`.
- **Proof.** On Pulse with the daily note saved, save `artifacts/$RUN_ID/home-pulse/home.aria.txt` and `home.png`. Both identify Arivu, heading `Home`, and `Daily note`. Confirm with `control-arivu db -- "SELECT body FROM daily_notes;"` containing `Ship the verification skill`.

## Gotchas

- Signup already lands on Home; do not treat a second navigation as required.
- Focus/Review/Board are empty on a fresh instance. Empty columns are valid; missing `#route-title` Home or missing view tabs are not.
- Saving the daily note is a mutation of today's note, not a standalone `/notes` item. Do not look for `Ship the verification skill` in the Notes list.
