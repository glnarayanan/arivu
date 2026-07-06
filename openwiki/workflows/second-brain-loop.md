# Second-Brain Loop

Arivu's working loop is:

1. Start from Today to plan the day and see the active loop.
2. Capture a bookmark or standalone note.
3. Triage it in Inbox by setting stage, importance, and next action.
4. Work from Focus when an item has tasks or reminders.
5. Let Review bring back high-signal, due, stale, or older material.

## Today

`/today` is the default signed-in landing page. It aggregates the existing
Inbox, Focus, Review, recent notes, and memory-jogger surfaces without replacing
their deeper workflows. Each day has one dated daily note at
`/api/daily-notes/{YYYY-MM-DD}` for planning, decisions, and loose thoughts.
Daily notes are scoped to the authenticated user and use the same web CSRF and
write-quota protections as normal notes.

## Capture

`/dashboard` captures URLs with optional quick notes, quotes, and tags. It
keeps capture and search visible, while filters and saved-search management stay
behind disclosures so the first screen stays focused. `/notes` captures URL-free
thoughts. New bookmarks and notes enter Inbox.

## Inbox

Inbox stores stages as `inbox`, `processing`, `processed`, and `archived`.
The embedded UI labels these as Inbox, Working, Kept, and Archived so people do
not need to think in internal state names.
Single-item edits use `PATCH /api/inbox/{item}`. Bulk triage uses
`POST /api/inbox/bulk` with up to 100 `bookmark:<id>` or `note:<id>` entries.
The bulk response includes updated and failed rows so cross-user or stale items
do not block valid updates.

## Notes

`/notes` is the compact list. `/notes/:id` is the note workspace for editing,
tasks, reminders, explicit links, backlinks, and linking the note to bookmarks.
`/notes?note=<id>` remains a compatibility path and redirects to `/notes/:id`.

## Focus

`/focus` defaults to pending open loops. Views are available at:

- `/focus?view=pending`
- `/focus?view=overdue`
- `/focus?view=today`
- `/focus?view=upcoming`
- `/focus?view=completed`

## Review

Review prioritizes processed or processing items, high importance, explicit next
actions, due reminders, stale action items, older unreviewed notes, and the
resurfacing score. Each item returns `review_reasons` and `review_priority` so
the UI can explain why it came back.
Review cards and cited-answer citations can store recall feedback. Useful items
rank higher, not-useful and snooze-longer items rank lower, and never-resurface
items are omitted from future Review queues without deleting their source data.
