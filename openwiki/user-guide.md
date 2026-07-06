# User Guide

Arivu is a self-hosted second-brain app for saving web pages, notes, tasks, and
reminders in one local SQLite-backed workspace.

## Start Arivu

Run the server:

```bash
go run ./cmd/arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

Open `http://127.0.0.1:8080/auth`, create an account, then start from `/today`.

For production, run behind TLS and set `COOKIE_SECURE=true`,
`SECRET_KEY`, `APP_URL`, `ADMIN_EMAILS`, and `SIGNUPS_ENABLED=false`.

## Core Workflow

1. Start at `/today` to plan the day, check Inbox, see open loops, review older
   signals, and update the dated daily note.
2. Capture a URL from `/dashboard`, the browser extension, the PWA share target,
   or `arivu save`.
   Dashboard captures made while offline are held in this browser and synced
   when the signed-in session is online again.
   Where supported by the browser, Dictate can append speech-to-text into daily
   notes, quick notes, standalone notes, and bookmark-linked notes.
3. Triage new saves and notes in `/inbox` by setting stage, priority, and next
   action.
4. Work from `/focus` when a save has a task or reminder.
5. Review older, due, high-priority, or still-actionable items in `/review`.

The UI labels item stages as Inbox, Working, Kept, and Archived. The stored API
values are `inbox`, `processing`, `processed`, and `archived`.

## Saving And Reading

Dashboard captures a URL with optional quick note and tags. Arivu archives the
page through the server, sanitizes readable HTML, creates a visible processing
job, and opens the saved bookmark.

Bookmark pages are reader-first. The top surface is for reading, tags,
summary/enrichment state, and source controls. The next-step panel captures the
one thing the item should become. Annotations, linked notes, explicit links,
tasks, reminders, and related items stay in collapsed workbench groups until
needed.

## Notes, Links, Tasks, And Reminders

Use `/notes` for standalone thoughts that do not start from a URL. `/notes/:id`
is the full note workspace for editing, tasks, reminders, note links, backlinks,
and note-to-bookmark links.

Use `/today` for the daily operating note. It is intentionally separate from the
standalone note list: one dated note per user per day, optimized for planning
and end-of-day wrap-up rather than long-term knowledge pages.

Use the Actions button or `Cmd/Ctrl+K` for the command palette. It opens core
routes, saves a URL, creates a note, runs search or cited-answer mode, and can
add a task, reminder, or explicit link when a bookmark or note is already open.

Tasks are undated checklist items. Reminders have due times, timezone metadata,
optional recurrence, and in-app due state. Email reminders are sent only when
Resend is configured.

## Search And Assistant

Dashboard search finds saved pages and supports filters for tags, domain,
source, read status, and saved date. Cited answers use only saved Arivu content.
Search and cited-answer citations explain why an item appeared and accept
feedback such as Useful, Not useful, Snooze longer, or Never resurface.

Settings import/export shows queued import progress, source counts, fetched,
AI-processed, and failed totals. Failed totals mean the source row was accepted
but the background fetch or processing step did not complete.

The Assistant page creates reviewable drafts for allowlisted actions: item state
updates, links, reminders, and action items. Drafts do not execute until a user
queues and runs the proposal.

## Import, Export, And Migration

Settings can import browser, Pocket, Raindrop, Linkwarden, OPML, RSS/Atom,
URL-bearing Readwise/Kindle CSV or TSV, Arivu JSON, or one URL per line.
Exports include JSON backup, CSV, browser HTML, Markdown, and Obsidian ZIP vault
output.

The same Settings import tab can also turn documents and media transcripts into
searchable notes. Upload an EPUB, PDF, plain text, Markdown, HTML file, or image,
or paste a YouTube/video transcript or OCR text. Arivu stores the result as a
normal note with a `media:*` source so it appears in Inbox, search, export, and
review. Image uploads use pasted OCR text when provided; if Gemini is configured,
Arivu can attempt image text extraction automatically.

Legacy Arivu migrations use the JSON export path documented in
`domain/migration-guide.md`.

## Useful Admin Notes

Admins are listed in `ADMIN_EMAILS`. Admin users can manage provider settings,
inspect audit events, and review system status from `/admin`.

Provider integrations are optional. Without Gemini, Arivu still stores
bookmarks, notes, tags, links, search, review, tasks, reminders, imports, and
exports using local deterministic processing.
