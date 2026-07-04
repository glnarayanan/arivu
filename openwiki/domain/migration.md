# Legacy Ingestion & Migrations

Arivu supports zero-downtime, validated migrations from vintage implementations. It includes legacy schemas import tools directly within the single binary.

---

## Porting from the Legacy Implementation

The historic database stack utilized MongoDB and FastAPI. Users can extract these active databases to standardized structured JSON documents and ingest them instantly.

- **Command Syntax**:
  ```bash
  ./arivu migrate --json-export /path/to/legacy-export --out migration-manifest.json --dry-run
  ./arivu migrate --json-export /path/to/legacy-export --dry-run=false --sqlite-db arivu.sqlite3 --old-secret-key "$OLD_SECRET_KEY" --new-secret-key "$SECRET_KEY"
  ```

---

## Legacy Export Validation and Handling

Inside `/internal/migrate/apply.go`, the migration engine enforces sequential verification steps to ensure no malformed data pollutes the new database:

### 1. Rejection of Unknown Fields
Unlike lenient schema-free database models, the modern JSON unmarshaller strict-parses inputs, immediately raising errors if any undocumented properties are detected in the import files.

### 2. ID Validation & Correlation
Validates that bookmark ids conform to correct string specifications. Structural cross-links (e.g., verifying bookmarks aligned inside collections actually points to legitimate objects) are resolved in-memory before bulk ingesting.

### 3. Fernet Secrets Decryption & Envelope Transformation
- **The Problem**: Legacy settings saved transactional API keys and SMTP passwords using Python's symmetrical Fernet encryption algorithms.
- **The Solution**: The Go migration tool reads the legacy `SECRET_KEY`, reconstructs the Fernet key blocks, decrypts legacy dynamic configuration, and safely re-encrypts it into the new envelopes managed by `/internal/secrets/`.

### 4. Forced Session Invalidation
To minimize attack surfaces, all legacy active OAuth and user cookie session states are discarded during migrations. All migrating users must run a fresh verification login sequence upon first accessing the modern Go deployment container.
