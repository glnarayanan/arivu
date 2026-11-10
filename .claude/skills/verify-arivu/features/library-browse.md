# Browse library

Library lists saved bookmarks, notes, daily notes, annotations, and objects, with filters and a Capture action. The default view is saved items, not generated entities.

## Sub-features

- `library-open` opens `/library` from Primary nav.
- `library-lists-note` shows a previously captured standalone note.
- `library-open-item` opens that note from the row `Open` link.
- `library-saved-vs-derived` distinguishes `Saved items` from `Concepts & entities`.

## How to get to it (user POV)

- Choose `Library` in the Primary nav.
- Open `/library` directly.
- Compatibility: `/dashboard` is Library capture, `/inbox` is a Library inbox view, `/objects` is objects in Library. Those are extra entry points, not a substitute for `/library`.

## Driving it with control-arivu

Preconditions:

- Isolated instance is healthy and the verify user is signed in.
- Note `Release checklist` exists from [Capture a note](./capture-note.md).

- **Open Library.** Choose `Library`. `#route-title` reads `Library`. `nav` named `Library view` has `Saved items` current. Region `Library items` is present. Button `Capture` is present.
- **See the note.** The list includes `Release checklist` (or an `Open Release checklist` link). Meta text may include `Capture:` for bookmarks; notes still appear as saved items.
- **Open item.** Choose `Open` for `Release checklist`. The note detail heading reads `Release checklist`.
- **Derived view.** Choose `Concepts & entities`. The URL contains `scope=derived`. This view is allowed to be empty on a fresh instance.
- **Proof.** On `/library` with the note visible, save `artifacts/$RUN_ID/library-browse/list.aria.txt` and `list.png`. Both identify Arivu, heading `Library`, and `Release checklist`. Save `control-arivu api GET /api/library/items?scope=content&limit=48` as `items.json`.

## Gotchas

- Default Library scope is `content`. An empty Concepts & entities view does not mean Library is broken.
- `/library?view=capture` is the richer Dashboard capture surface, not the list. Do not prove listing there.
- Cursor pagination (`limit` 48) is unused on a single-note fixture; do not invent a second page.
- Compatibility routes must keep incoming query strings. If you open `/inbox`, assert the canonical Library URL rather than treating the alias as the destination name.
