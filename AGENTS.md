# Agent Notes

Keep this file lean. Load focused docs under `documentation/` when needed.

## Canonical Docs

- Project overview: `README.md`
- Design context: `documentation/design-context.md`
- Architecture: `documentation/architecture.md`
- Deployment: `documentation/deployment.md`
- Security model: `documentation/security-model.md`
- Dependency policy: `documentation/dependency-policy.md`
- SQLite schema: `documentation/sqlite-schema.md`
- Migration guide: `documentation/migration-guide.md`
- Contribution guide: `CONTRIBUTING.md`
- Security policy: `SECURITY.md`
- Changelog: `CHANGELOG.md`

## Working Rules

- Keep long-form project docs in `documentation/`.
- Update relevant docs and `CHANGELOG.md` when behavior, setup, security, or operational guidance changes.
- Run `gofmt` on changed Go files.
- Run `GOCACHE=/private/tmp/arivu-build-cache go test ./...` before handoff when possible.
- The shipped frontend is embedded under `internal/app/web` and should stay dependency-free.
