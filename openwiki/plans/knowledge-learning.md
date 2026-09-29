# Focused knowledge and learning

This is a hand-maintained implementation plan, not a generated OpenWiki page.

## Product scope

Arivu will help people capture, understand, remember, and reuse saved sources
and personal notes. Keep the light-only design, self-hosting, export, source
evidence, provider-free reading and search, and existing security boundaries.

Retire task and recurring-reminder management, Board and Focus planning views,
priority and next-action tracking, typed object workflows, and calendar import.
Convert useful existing records into notes without discarding their exact
metadata. Daily notes become dated notes. Keep connections and useful
resurfacing, with Graph secondary and no standalone Insights dashboard.

Add source-grounded conversations, saving answers into notes with evidence,
and short optional quizzes. Start conversations with one source, then support
selected sources and library retrieval. No general-purpose agent, automatic
actions, mandatory study queue, or model menu in the main workflow.

## Delivery order

The stack starts above the existing performance PRs. Each slice includes focused
tests and its documentation; visual changes also need desktop and mobile checks.

1. Preservation schema, atomic conversion, and versioned export/restore.
2. Activate conversion and retire the old workflows, routes, jobs, and controls.
3. Source-grounded conversations with inspectable passages and provider consent.
4. Selected-source and library scope, retaining follow-up context.
5. Save answers or passages into editable notes with durable provenance.
6. Optional quizzes with server-held answers and cited explanations.
7. Contextual recall and cross-feature verification.
8. Update the marketing repository against the tested app, with new screenshots
   and separate Chronicle posts where the subject warrants them.

## Preservation foundation

`knowledge_preservation` retains an owner-scoped snapshot of each legacy row,
including nulls, timestamps, structured fields, and assistant proposals.
`PreserveKnowledgeWorkflows` writes snapshots, converted notes, and owned
source links in one transaction. Existing rows remain untouched. A retained
mapping prevents repeated conversion from overwriting edited notes or
recreating deleted ones.

The retirement slice activates conversion at startup and indexes the converted
notes before serving requests. It removes planning controls and returns an
authenticated `410 Gone` for old workflow APIs. Old reminder jobs finish without
sending mail. Home, Library, Notes, and Search are primary; Review and Graph are
secondary. Daily writing now uses ordinary notes.

Full JSON exports use version 3 and include inert snapshots. Restore
remaps converted note references to the receiving account and retains deleted
note mappings. Unknown future backup versions fail before writes. Versions 1
and 2 remain accepted. Foundation-stage version-3 files may contain both legacy
rows and preservation records; existing preservation identities take precedence.
Legacy imports create notes and snapshots, never active workflows or reminder
jobs. This does not make all older restore helpers atomic.

## Acceptance boundaries

- No user can retrieve another user's sources, generated content, or answers.
- Search snippets and generated summaries are not original source evidence.
- Provider requests disclose the destination and the selected content; local
  library scope does not imply local model processing.
- Source text cannot issue instructions, execute tools, or create content.
- Generated answers remain distinct from personal writing and original sources.
- Capture, offline queues, extension/CLI audiences, backup, and export still work.
- Marketing claims distinguish measured local retrieval from provider latency.
- Keep migration, routes, and frontend integration in one checkout. Helpers may
  own isolated fixtures or provider code after contracts are fixed.

## Conversation and quiz contract

`/learn` lists saved sessions and finds passages by topic. Reader and note
actions start with one source; Library supports up to eight selected sources.
The preview saves up to 16 exact passages, each at most 1,500 bytes, from the
selected original capture or a non-AI note. Search snippets and summaries may
help find candidates but never become evidence. Review remains optional
resurfacing, not a mandatory study queue.

Generation sends those passages to the configured provider only after explicit
consent. The preview names the provider, host, and model; a destination change
requires fresh consent. Before each request, Arivu checks source ownership,
content hashes, and every passage against the current text. Imported sessions
receive the same checks. Self-hosting does not mean local model processing.

Conversations allow six exchanges and include up to 12,000 bytes of recent
full turns as context, not evidence. Every claim must cite an exact quote, or
the response must state insufficient evidence. Exact-quote checks do not prove
that a claim follows from the quote. The UI labels generated content and asks
users to check it. Models have no tool or action interface.

Quizzes contain three questions with four options each. Normal API responses
withhold correct answers, explanations, and citations until all choices are
submitted. Full owner backups retain them. AI answers saved to Notes carry an
`ai:answer` marker and cannot feed later sessions as original evidence.
Saved passages and answers retain quoted text and owned source links.

Sessions live in owner-scoped `learning_sessions`. Revision checks prevent
concurrent results from overwriting one another; concurrent provider requests
may still both run. Deleting a session removes its excerpts but not notes
already saved from it. Deleting a source blocks further generation, but does
not erase prior session excerpts. Full backups include sessions and restore
source references; identical snapshots deduplicate while distinct snapshots
remain separate. Restore preserves exact note text, including whitespace.

The web routes retain CSRF and audience checks. Generation is limited to 30
requests per hour, with a 40-second provider timeout and bounded input/output.
Provider-free reading, search, capture, and passage saving remain available.
