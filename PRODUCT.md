# Arivu Product

Arivu is a self-hosted second brain for saved sources and personal notes. It runs
from one Go binary with SQLite and remains useful without an AI provider.

## Core loop

Capture, understand, remember, and reuse.

- Save links, notes, quotes, and files without choosing a folder or provider.
- Read preserved sources, write notes, and follow explicit links and backlinks.
- Search saved material and revisit useful items without a task queue.
- Ask questions about original passages, inspect quotes, and save answers as
  editable notes with source links.
- Try an optional three-question quiz, then check source-backed explanations.
- Inspect derived connections in the secondary Graph view.

Home, Library, Notes, and Search are the primary destinations. Review and Graph
sit under More. Library separates saved content from derived entities and
concepts. Capture remains available throughout the app.

## Boundaries

Arivu is not a task manager or an autonomous agent. Board, Focus, recurring
reminders, priority/stage/next-action controls, typed objects, calendar import,
and the standalone Insights dashboard are retired. Existing writing and useful
context become ordinary notes; exact legacy records remain in full backups.
Authenticated legacy API calls return `410 Gone`, rather than accepting work
that will never run. Queued reminder emails no longer send.

Conversations and quizzes use one source, up to eight selected sources, or
passages found in the library. The user previews the text and provider before
sending it. Sessions keep a fixed set of passages; changing a source requires
a fresh preview. There is no study schedule or automatic action.

## Guarantees

- AI is optional. Reading, capture, notes, search, and links work without it.
- AI replies cite exact quotes, but a matching quote does not prove the claim.
  Users must check the evidence. Generated answers never serve as original
  evidence for another conversation or quiz.
- A generated summary is not original source evidence. Unsupported claims must
  not become source facts; incomplete captures may have no generated summary.
- Queries, sources, notes, and derivatives belong to the signed-in user.
- Server-side HTML sanitization, SSRF checks, CSRF, and separate web, CLI, and
  extension session audiences remain in force.
- Capture channels, offline capture queues, full export, and self-hosting stay.
- Editing or deleting a converted note survives restart and repeat import.
- Explicit links are durable. Derived connections can be rebuilt; their
  provenance and user feedback remain inspectable.
- The embedded frontend stays dependency-free, accessible, and light-only.

The visual system lives in [DESIGN.md](DESIGN.md). The detailed retirement and
backup contract lives in the [implementation plan](openwiki/plans/knowledge-learning.md).
