# Documentation

This folder keeps durable project docs: runbooks, policies, status, design
context, and migration details. For richer codebase orientation, source maps,
and workflow walkthroughs, start with `../openwiki/quickstart.md`.

## Repository Status

This repository, `glnarayanan/arivu`, is the active Arivu codebase. The previous
Python/FastAPI/MongoDB/React implementation is archived at
`glnarayanan/arivu-legacy` for historical and migration reference.

- `STATUS.md`: implementation and verification status.
- `deployment.md`: local, Docker, and systemd deployment.
- `dependency-policy.md`: accepted dependencies and supply-chain posture.
- `design-context.md`: embedded frontend product and visual direction.
- `frontend-runtime.md`: embedded frontend conventions and browser smoke checks.
- `migration-guide.md`: legacy export validation and SQLite import.
- `security-model.md`: security controls and trust boundaries.
- `sqlite-schema.md`: persistence model and FTS fallback.
