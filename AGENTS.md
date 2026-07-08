# Agent Notes

Keep this file lean. Load focused docs under `openwiki/` when needed.

## OpenWiki

This repository has documentation located in the /openwiki directory.

Start here:
- [OpenWiki quickstart](openwiki/quickstart.md)

OpenWiki includes repository overview, architecture notes, workflows, domain concepts, operations, integrations, testing guidance, and source maps.

When working in this repository, read the OpenWiki quickstart first, then follow its links to the relevant architecture, workflow, domain, operation, and testing notes.

## Working Rules

- Keep long-form project docs in `openwiki/`.
- Update relevant docs and `CHANGELOG.md` when behavior, setup, security, or operational guidance changes.
- Run `gofmt` on changed Go files.
- Run `GOCACHE=/private/tmp/arivu-build-cache go test ./...` before handoff when possible.
- The shipped frontend is embedded under `internal/app/web` and should stay dependency-free.
