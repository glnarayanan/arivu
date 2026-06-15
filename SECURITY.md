# Security Policy

Report vulnerabilities privately to the project maintainer before public disclosure.

## Supported Version

The supported codebase is the standalone Go implementation in this repository. The archived legacy repository is kept for migration reference only.

## Security Model Highlights

- Opaque access and refresh tokens are hashed at rest.
- Session audiences are enforced for web, CLI, and extension routes.
- Cookie-authenticated mutations require CSRF protection.
- Outbound fetches reject private, loopback, link-local, reserved, multicast, and unspecified IP targets.
- HTML archives are sanitized server-side and served with a restrictive CSP.
