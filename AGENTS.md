# Agent Notes

Keep this file lean. Load focused docs under `openwiki/` when needed.

## Documentation

Start with [OpenWiki quickstart](openwiki/quickstart.md) for codebase
orientation, runbooks, policy, design, migration, and reference docs.

## Working Rules

- Keep long-form project docs in `openwiki/`.
- Update relevant docs and `CHANGELOG.md` when behavior, setup, security, or operational guidance changes.
- Run `gofmt` on changed Go files.
- Run `GOCACHE=/private/tmp/arivu-build-cache go test ./...` before handoff when possible.
- The shipped frontend is embedded under `internal/app/web` and should stay dependency-free.
