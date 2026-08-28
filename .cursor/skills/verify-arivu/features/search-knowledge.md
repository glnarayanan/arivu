# Search knowledge

Search lets a user find owned notes by title or body from Search / Ask, open a result, and distinguish no matches from an empty query.

## Sub-features

- `search-open` opens Search / Ask from the top bar and from `/`.
- `search-match` returns the seeded note without changing it.
- `search-open-result` opens the matching note.
- `search-empty` shows the no-match empty state.
- `search-ask` keeps Ask as a separate submit; cited answers require saved material (and may be thin without a model provider).

## How to get to it (user POV)

- Choose `Search / Ask` in the top bar.
- Press `/` while focus is outside an editable field.
- Open `/search` directly.
- From Home Fast capture, choose `Ask Arivu` (Ask mode).

## Driving it with control-arivu

Preconditions:

- Isolated instance is healthy.
- The verify account exists and the note `Release checklist` with body `Tag and publish` was created via [Capture a note](./capture-note.md).
- `control-arivu doctor` passes.

- **Toolbar entry.** Choose `Search / Ask`. `#route-title` reads `Search / Ask`. Focus is in `Search your knowledge`.
- **Title match.** Fill `Search your knowledge` with `Release` and choose `Search`. The region `Search results` contains a heading link `Release checklist` and does not claim `No match`.
- **Open result.** Choose `Release checklist`. The note detail heading reads `Release checklist`.
- **Empty state.** Return to `/search`, fill `volcano-not-in-arivu`, choose `Search`. Empty state title `Try a broader phrase` appears.
- **Clear / ready.** Open `/search` with no query. Empty state title `Start with what you remember` appears.
- **Proof.** On the populated result view, save `artifacts/$RUN_ID/search-knowledge/results.aria.txt` and `results.png`. Both identify Arivu, `Search / Ask`, and `Release checklist`. Save `control-arivu api GET /api/search/items?q=Release` as `results.json`.

## Gotchas

- Pressing `/` while a field has focus inserts a slash instead of navigating.
- Ask (`mode=ask`) is a different submit. A Search proof is not an Ask proof. Without a model provider, Ask may still return citations from saved text; do not require a fluent generated answer.
- Results are owned-content only. Searching the public web is out of scope.
- Do not seed the note through `POST /api/notes` and then claim Search works; the note must already exist from the capture-note user path.
