# Migration Guide

The migration implementation validates a legacy JSON export, then applies the
validated data into the SQLite database.

## Current Command

Dry-run discovery and validation:

```bash
go run ./cmd/arivu migrate --json-export ./legacy-export --out migration-manifest.json --dry-run
```

Apply a validated export into SQLite:

```bash
go run ./cmd/arivu migrate \
  --json-export ./legacy-export \
  --dry-run=false \
  --sqlite-db ./arivu.sqlite3 \
  --old-secret-key "$OLD_SECRET_KEY" \
  --new-secret-key "$SECRET_KEY" \
  --key-id migration-2026-06
```

The generated manifest documents how legacy fields map to SQLite:

- `column`
- `join table`
- `JSON blob`
- `derived`
- `dropped with reason`

Unknown legacy fields must fail dry-run validation instead of being silently
discarded.

## JSON Export Shape

The `--json-export` path is required and may be either:

- A single JSON object keyed by collection name, where each value is an object
  or array of objects.
- A directory containing collection-named files such as `users.json`,
  `bookmarks.json`, or `bookmarks.jsonl`.

The validator samples up to `--sample-limit` documents per collection, rejects
unknown collections and fields, and rejects missing required fields such as user
email or bookmark URL. The output manifest includes per-collection sample counts
under `samples`.

The apply command prints a JSON report with imported row counts, source document
counts, skipped legacy sessions, warnings, and errors.

## Apply Guarantees

- Requires the old `SECRET_KEY` to decrypt legacy Fernet-encrypted X tokens and runtime settings.
- Re-encrypts migrated X tokens and secret runtime settings with AES-256-GCM key material derived from the new `SECRET_KEY`; encrypted settings include the supplied `--key-id`.
- Stores plain runtime settings such as X redirect URI, X enablement, and Resend sender as plain settings.
- Preserves valid user, bookmark, collection, summary, and X connection IDs.
- Validates ownership and referential integrity for bookmarks, summaries, collections, collection memberships, and X connections.
- Sanitizes archived HTML during import.
- Validates embedding vectors as numeric JSON arrays and stores dimensionality.
- Normalizes bookmark entities, concepts, collection memberships, and access history into SQLite join tables.
- Intentionally drops legacy sessions and reports the count, requiring user reauthentication after cutover.
- Runs inside a transaction; orphaned rows, duplicate IDs, unknown fields, or secret decryption failures roll back the import.

The target database must be empty unless `--allow-existing` is passed. Use
`--allow-existing` only for controlled fixture merges; production migration
should start from an empty initialized SQLite database.
