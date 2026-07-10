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
needed. When you select text inside the reader, the annotation form can copy it
as a quote, store a text-quote selector, and later jump the saved annotation
back to the matching source text.

## Notes, Links, Tasks, And Reminders

Use `/notes` for standalone thoughts that do not start from a URL. `/notes/:id`
is the full note workspace for editing, tasks, reminders, note links, backlinks,
and note-to-bookmark links.

Use `/objects` when a note or bookmark needs to become a typed thing: project,
person, book, meeting, decision, or research thread. Objects have a title,
description, optional source item, and small JSON fields for local structure.
Use `/board` for a fixed working board that groups Inbox, Working, Review,
recent decisions, and recent meetings. Use `/evolution?q=topic` to see how a
topic appears across daily notes, saved pages, notes, meetings, decisions, and
objects over time.

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

After a successful online read, the browser keeps recent local snapshots for
Today, saved pages, notes, Review, Inbox/work queues, reminders, and typed
search. If the network drops later, those screens can show the latest local copy
instead of failing immediately. Writes still require the server except for the
dashboard URL capture queue.

Settings import/export shows queued import progress, source counts, fetched,
AI-processed, and failed totals. Failed totals mean the source row was accepted
but the background fetch or processing step did not complete.

Saved-page cards show a source description when one is available. A missing
description is shown as unavailable rather than as a queued enrichment job;
use the bookmark reader or import progress to inspect actual processing state.

The Assistant page creates reviewable drafts for allowlisted actions: item state
updates, links, reminders, and action items. Drafts do not execute until a user
queues and runs the proposal.

## Import, Export, And Migration

Settings can import browser, Pocket, Raindrop, Linkwarden, OPML, RSS/Atom,
URL-bearing Readwise/Kindle CSV or TSV, Arivu JSON, or one URL per line.
Exports include JSON backup, CSV, browser HTML, Markdown, and Obsidian ZIP vault
output. Full JSON backups preserve X bookmark identity and metadata, including
tweet ID, author handle/name, tweet URL, and available metrics.

The same Settings import tab can also turn documents and media transcripts into
searchable notes. Upload an EPUB, PDF, plain text, Markdown, HTML file, or image,
or paste a YouTube/video transcript or OCR text. Arivu stores the result as a
normal note with a `media:*` source so it appears in Inbox, search, export, and
review. Image uploads use pasted OCR text when provided; if the configured model
provider is Gemini, Arivu can attempt image text extraction automatically.

Calendar imports accept pasted ICS text and create meeting objects with UID,
start, end, location, description, and source fields. Full JSON backups include
knowledge objects and restore their source bookmark/note/object references under
the importing account.

CLI tokens can call the agent-oriented `/api/agent/*` routes for scoped search,
reading saved bookmarks/notes, creating notes, adding tasks/reminders, and
recording decisions. These routes are intended as the HTTP surface for a thin
MCP wrapper rather than a separate automation platform.

Legacy Arivu migrations use the JSON export path documented in
`domain/migration-guide.md`.

## Useful Admin Notes

Admins are listed in `ADMIN_EMAILS`. Admin users can manage provider settings,
inspect audit events, and review system status from `/admin`.

Provider integrations are optional. Without a configured model provider, Arivu
still stores bookmarks, notes, tags, links, search, review, tasks, reminders,
imports, and exports using local deterministic processing.

When changing Model Provider, enter the model for providers without a preset
default and provide a new API Key for authenticated services. LM Studio,
Ollama/local, and Custom may be used without an API key. Arivu replaces the
previous provider's model, Base URL, and credential state during the switch.
