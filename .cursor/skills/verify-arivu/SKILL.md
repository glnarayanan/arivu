---
name: verify-arivu
description: Drive Arivu's embedded web app (and isolated CLI) the way a user does. Use when proving a change, checking capture/search/library/home/graph, or verifying auth and persistence against a real Go+SQLite instance.
---

# Verify Arivu

Arivu is a single Go binary: embedded browser UI, JSON API, SQLite, and a CLI. The primary user surface is the browser app. The CLI (`login`, `save`, `list`, `search`) and the companion extension are secondary. This skill drives a disposable instance; it does not touch the operator's `arivu.sqlite3` or `~/Library/Application Support/arivu/config.json`.

Read `features/README.md` before driving. A proof that uses one convenient entry point is incomplete when the map lists others.

## Launch

From the repository root:

```bash
chmod +x .cursor/skills/verify-arivu/scripts/control-arivu
.cursor/skills/verify-arivu/scripts/control-arivu launch
export ARIVU_VERIFY_RUN_ID=<printed RUN_ID>
.cursor/skills/verify-arivu/scripts/control-arivu doctor
```

`launch` builds `cmd/arivu` into `/tmp/arivu-verify-$RUN_ID/arivu` with `CGO_ENABLED=1` (required by `go-sqlite3`), then starts `serve` in a new session so the helper can exit:

- listen `127.0.0.1:<ephemeral port>` — never default `:8080`
- SQLite `/tmp/arivu-verify-$RUN_ID/arivu.sqlite3`
- `APP_URL` matching that origin, `SIGNUPS_ENABLED=true`, `COOKIE_SECURE=false`, `ARIVU_BROWSER_CAPTURE_ENABLED=false`, unique `SECRET_KEY`

Ready when `GET /api/health` returns `{"status":"ok","stack":"go-sqlite",...}` and the serve log contains `arivu listening on 127.0.0.1:<port>`.

Seed account (browser path, required for first-run proofs): open `{URL}/auth`, fill Email `verify@arivu.test`, Password `VerifyPass9` (min 8 chars), choose **Create account**. Expect toast `Account created` and heading `Home`. For API/CLI-only recipes after that account exists, `control-arivu signup` or `control-arivu login` is allowed as a second session, not as a substitute for a UI proof of signup.

Teardown: `control-arivu stop` (kills this run's PID only, deletes `/tmp/arivu-verify-$RUN_ID` and the run state file).

## Doctor

Run `control-arivu doctor` first whenever anything looks off. It is read-only besides reading cookies. It must report:

- the PID from state is alive
- that PID owns the listen port (`lsof`)
- `/api/health` is `status=ok` and `stack=go-sqlite`
- `$BIN --version` prints `arivu …` and `$BIN` is this run's `/tmp/arivu-verify-$RUN_ID/arivu`
- `GET /auth` is 200
- if a cookie jar exists, `GET /api/auth/me` returns the verify email

Refuse to drive `http://127.0.0.1:8080` (or any other URL) unless doctor says this run's PID owns it. Two instances can run side by side because each run picks its own port, database, secret, and `HOME` for CLI. Never attach to a shared instance.

## Drive

Harness: Playwright MCP (preferred) or Cursor IDE browser against `{URL}`, plus `control-arivu` for doctor, API second views, SQLite side effects, and isolated CLI. If a harness blocks the **Create account** button (false-positive signup policy), use `control-arivu signup` then the browser **Sign in** path for that account step only; still prove note mutations in the UI.

Stable handles (from `internal/app/web/app.js`):

| Action | Handle |
|---|---|
| Brand / home | link `Arivu home` → `/today` |
| Primary nav | `nav` named `Primary`: Home, Library, Notes, Graph, Insights |
| Capture | button `#global-capture` accessible name starts with `Capture` (shortcut `Q`) |
| Search / Ask | link `Search / Ask` → `/search` (shortcut `/`) |
| Profile | button `Open profile and settings menu` |
| Auth | `/auth`, labels `Email` / `Password`, buttons `Sign in` and `Create account` |
| Capture dialog | `role=dialog` labelled `Capture`; `#capture-kind` options Link/Note/Quote/File; submit `Save to Arivu`; cancel `Cancel`; close `Close dialog` |
| Notes workspace | `/notes`, form heading `New note`, labels `Title` / `Body`, submit `Save note` |
| Note detail | heading is the title; link `All notes`; buttons `Save changes` / `Delete note` |
| Search | `/search`, heading `Search / Ask`, label `Search your knowledge`, buttons `Search` and `Ask` |
| Library | `/library`, heading `Library`, button `Capture`, region `Library items` |
| Graph | `/graph`, heading `Graph`, `Accessible node list`, region `Interactive knowledge graph` |
| Home | `/today`, heading `Home`, `Daily note`, submit `Save daily note` |
| Toasts | `role=status` text `Account created`, `Signed in`, `Captured`, `Note saved` |

CLI (isolated `HOME` only):

```bash
.cursor/skills/verify-arivu/scripts/control-arivu cli -- login --api "$URL/api" --email verify@arivu.test --password VerifyPass9
.cursor/skills/verify-arivu/scripts/control-arivu cli -- save "https://example.com/verify-link"
.cursor/skills/verify-arivu/scripts/control-arivu cli -- list
.cursor/skills/verify-arivu/scripts/control-arivu cli -- search "verify-link"
```

`save`/`list`/`search` are bookmark-only. Notes are not a CLI surface.

API second view (same account, not the primary user path):

```bash
.cursor/skills/verify-arivu/scripts/control-arivu login
.cursor/skills/verify-arivu/scripts/control-arivu api GET /api/notes
.cursor/skills/verify-arivu/scripts/control-arivu api GET /api/library/items?scope=content&limit=48
.cursor/skills/verify-arivu/scripts/control-arivu api GET /api/search/items?q=Release
```

Mutating `/api/*` is not a UI proof. Use it only as a second read of stored state, or when a feature file's preconditions explicitly seed data after a prior mapped UI path.

## Evidence

Write under `.cursor/skills/verify-arivu/artifacts/$RUN_ID/<feature>/`. That directory survives `stop`.

Proof standards:

- Exercise the real user path (browser chrome, or CLI subcommands a user runs). Do not call internal setters or test-only endpoints as the action under test.
- Capture the action and the resulting state, not only the final screen.
- UI proof: accessibility snapshot plus screenshot with the `Arivu` brand and `#route-title` visible.
- Mutation proof: reopen from a list or a second surface, and confirm SQLite (`control-arivu db -- "SELECT title FROM notes;"`) or `GET /api/notes`.
- CLI proof: command, stdout, stderr, exit code. Bookmark CLI output is `id\ttitle\turl`.
- Record the feature ID and entry point with every artifact.
- Link capture hits the live SSRF-shielded fetcher. Prefer note capture for proofs that must not depend on outbound HTTP. If a recipe does save a URL, use a distinct URL and wait for the bookmark page heading; do not treat a toast alone as persistence.
- Browser-capture preservation (`ARIVU_BROWSER_CAPTURE_ENABLED`) stays off in this harness. Do not claim rendered artifacts exist.
- Mocks only at production boundaries that already isolate external systems (none are required for note capture, search of owned text, library, home, or empty graph).

## Cleanup

```bash
.cursor/skills/verify-arivu/scripts/control-arivu stop
```

Kills only the PID in this run's state file. Deletes `/tmp/arivu-verify-$RUN_ID` and `.cursor/skills/verify-arivu/runs/$RUN_ID`. Does not delete `.cursor/skills/verify-arivu/artifacts/`. Does not kill by process name `arivu`. After a failed attempt, run `stop` before launching again so ports and temp dirs are not stranded.

## Helpers

`.cursor/skills/verify-arivu/scripts/control-arivu` is executable. Subcommands: `launch`, `doctor`, `env`, `signup`, `login`, `api`, `db`, `cli`, `stop`. It resolves the repo root from its path. Pass `ARIVU_VERIFY_RUN_ID` or it uses the newest run directory.
