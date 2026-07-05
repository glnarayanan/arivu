# Second-Brain Loop

Arivu's working loop is:

1. Capture a bookmark or standalone note.
2. Triage it in Inbox by setting stage, importance, and next action.
3. Work from Focus when an item has tasks or reminders.
4. Let Review bring back high-signal, due, stale, or older material.

## Capture

`/dashboard` captures URLs with optional quick notes, quotes, and tags.
`/notes` captures URL-free thoughts. New bookmarks and notes enter Inbox.

## Inbox

Inbox stages are `inbox`, `processing`, `processed`, and `archived`.
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
the UI can explain why it appeared.
