# Arivu verification map

This directory is the maintained source for verifying Arivu's user-facing behavior. Read this index before driving the app, then use the matching feature file as the recipe.

## Baseline preconditions

- Launch with `.cursor/skills/verify-arivu/scripts/control-arivu launch` and export `ARIVU_VERIFY_RUN_ID`.
- Run `control-arivu doctor` and require the printed URL, this run's `/tmp/arivu-verify-$RUN_ID` database, and this run's binary.
- Never drive an instance that was not started by this verification run. In particular, do not open `http://127.0.0.1:8080` unless doctor says this run owns that port.
- Create the verify user on `{URL}/auth` with Email `verify@arivu.test` and Password `VerifyPass9` via **Create account**, unless a recipe's preconditions say the account already exists.
- Keep `ARIVU_BROWSER_CAPTURE_ENABLED=false`. Note capture and local search do not need a model provider.

## Driving conventions

- Start every recipe from the baseline state unless its preconditions say otherwise.
- Prefer accessible names, `#route-title`, and the IDs in the skill over CSS position or coordinates.
- Treat every command as literal. Keep emails, titles, and flags unchanged.
- Run browser actions through Playwright MCP (or Cursor IDE browser) against the URL from `control-arivu env`.
- Run terminal actions through `control-arivu cli --` (isolated `HOME`) or `control-arivu api` / `db` for second reads.
- Restore seeded data after a mutation only when the recipe says so. Do not remove proof artifacts during cleanup.

## Proof and skip reporting

- Capture the user action and the resulting state, not only the final screen.
- UI proof includes an ARIA snapshot and a screenshot with the Arivu brand visible.
- CLI proof includes the command, stdout, stderr, and exit code.
- Mutation proof includes a second user-facing view and a stored-value check (`GET /api/notes` or `control-arivu db`).
- Record the feature ID and entry point used with every artifact.
- Report an unreachable path with the attempted command and the unmet precondition.
- Do not report a skipped entry point as verified through a different path.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the user-visible behavior. It then uses exactly four H2 sections in this order: `Sub-features`, `How to get to it (user POV)`, `Driving it with control-arivu`, `Gotchas`.

## Features

- [Capture a note](./capture-note.md) covers Create account, global Capture, the Notes workspace, and persistence.
- [Search knowledge](./search-knowledge.md) covers Search / Ask retrieval of an owned note.
- [Browse library](./library-browse.md) covers Library listing of saved notes.
- [Home pulse](./home-pulse.md) covers Home, daily note save, and Recent notes.
- [Inspect graph](./graph-inspect.md) covers the bounded Graph destination and accessible node list.
