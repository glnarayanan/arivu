# Legacy Migration

Arivu migrates from a legacy JSON export into the SQLite database used by the Go
application. The migration code lives in `internal/migrate`.

## Commands

```bash
./arivu migrate --json-export /path/to/legacy-export --out migration-manifest.json --dry-run
./arivu migrate --json-export /path/to/legacy-export --dry-run=false --sqlite-db arivu.sqlite3 --old-secret-key "$OLD_SECRET_KEY" --new-secret-key "$SECRET_KEY"
```

## Validation

- Unknown collections and unknown fields fail validation.
- Required user, bookmark, collection, summary, and X connection fields are checked before import.
- Bookmark, summary, collection, membership, access-history, and X connection relationships are validated before commit.
- Archived HTML is sanitized during import.
- Embedding vectors must be numeric arrays.

## Secrets

Legacy X tokens and runtime provider settings are decrypted with the old
`SECRET_KEY`, then written with the Go app's settings model:

- provider secrets are re-encrypted with `internal/secrets`
- plain settings such as redirect URI, Resend sender, and X enablement are
  stored as plain runtime settings

Legacy sessions are not migrated. Users sign in again after cutover.
