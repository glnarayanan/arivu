# Security Policy

If you discover a security vulnerability in Arivu, please report it responsibly.

**Email:** security@arivu.app

**Do not** open a public GitHub issue for security vulnerabilities.

## Supported Version

The supported codebase is the standalone Go implementation in this repository. The archived legacy repository is kept for migration reference only.

## What to Include

- Description of the vulnerability.
- Steps to reproduce.
- Affected components, such as server, embedded frontend, extension, migration tooling, deployment, or provider integration.
- Potential impact.
- Any proof-of-concept details that help reproduce the issue safely.

## Response Timeline

- **Acknowledgment:** Within 48 hours.
- **Initial assessment:** Within 1 week.
- **Fix timeline:** Depends on severity; critical issues are prioritized.

## Scope

The following components are in scope:

- Go server and API routes under `cmd/` and `internal/`.
- Embedded frontend under `internal/app/web`.
- Browser extension under `extension/`.
- Migration tooling for legacy exports.
- Deployment configurations under `Dockerfile` and `deploy/`.
- Authentication, authorization, session audience, CSRF, SSRF, sanitizer, and secret-handling controls.

## Out of Scope

- Third-party services such as Gemini, Resend, X, and hosting providers.
- Issues in dependencies unless they create a reachable vulnerability in Arivu.
- Vulnerabilities requiring local host compromise or privileged shell access without an Arivu-specific privilege escalation.

## Security Model Highlights

- Opaque access and refresh tokens are hashed at rest.
- Session audiences are enforced for web, CLI, and extension routes.
- Cookie-authenticated mutations require CSRF protection.
- Outbound fetches reject private, loopback, link-local, reserved, multicast, and unspecified IP targets.
- HTML archives are sanitized server-side and served with a restrictive CSP.
- Runtime settings and provider credentials are encrypted at rest when stored in SQLite.

## Disclosure

We will coordinate disclosure with the reporter. We aim to release fixes before public disclosure.
