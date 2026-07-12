# Deployment

Arivu’s primary self-hosting path is the first-party installer CLI. It prepares
a Linux VPS end to end, while preserving unrelated apps on shared hosts.

## One-Command Install

```bash
curl -fsSL https://install.arivu.app | sudo bash
```

Pin a release when you need reproducible installs:

```bash
curl -fsSL https://install.arivu.app | sudo ARIVU_VERSION=v1.2.3 bash
```

The bootstrap script downloads `arivu-installer`, verifies it against the
release `SHA256SUMS`, and installs it under `/usr/local/bin`. On a fresh host it
starts the interactive installer. When `/etc/arivu/arivu.env` already exists,
the same command runs an upgrade instead, preserving the existing setup. No
GitHub CLI login or token is required on the target host.

The installer asks for:

- Domain or subdomain.
- First admin email and password.
- TLS notification email.
- Proxy mode.
- Signup policy.
- Backup policy.
- Optional provider settings later through Admin > Settings.

## Shared VPS Behavior

The installer runs preflight before changing anything. It detects existing
listeners on ports 80 and 443, Caddy, Nginx, Apache, Docker, UFW/firewalld,
existing Arivu files, and domain vhost conflicts.

Proxy modes:

- `auto`: choose the safest mode from detected host state.
- `managed-caddy`: install an Arivu-owned Caddy site block on clean hosts,
  add the Arivu `conf.d` import if needed, validate Caddy, and reload it. If
  UFW or firewalld is detected, the installer prints exact additive commands
  for ports 80 and 443 and does not claim public HTTPS is complete until the
  operator runs them.
- `existing-proxy`: bind Arivu to `127.0.0.1:<free-port>` and write proxy
  snippets for the existing proxy. The installer validates supported proxy
  configs but leaves final attachment to the existing proxy owner. `existing`
  is accepted as a CLI alias.
- `app-only`: start Arivu on loopback and print Caddy, Nginx, and Apache
  examples without changing web server config.

Safety rules:

- The installer never replaces global Caddy, Nginx, or Apache config.
- The installer never stops unrelated services.
- Firewall changes are manual by default and additive only. The installer
  prints commands such as `sudo ufw allow 80/tcp` and `sudo ufw allow 443/tcp`
  instead of silently opening ports.
- If the requested domain already appears in an existing vhost, the installer
  stops and asks for a different domain or subdomain.

## Automation

```bash
sudo arivu-installer install \
  --non-interactive \
  --domain arivu.example.com \
  --admin-email admin@example.com \
  --admin-password-file /root/arivu-admin-password \
  --tls-email ops@example.com \
  --proxy-mode auto \
  --version latest
```

Preview changes without applying them:

```bash
arivu-installer plan --domain arivu.example.com --admin-email admin@example.com
```

Operational commands:

```bash
arivu-installer status --domain arivu.example.com
arivu --version
arivu-installer --version
sudo arivu-installer backup
sudo arivu-installer restore --backup /var/backups/arivu/20260708T010203Z
sudo arivu-installer upgrade
sudo arivu-installer reconfigure
sudo arivu-installer uninstall
```

Release builds inject the Git tag into both binaries. A packaged release should
report the same tag for the application and installer; `devel` is reserved for
untagged local builds. Install and upgrade write app and installer binaries with
explicit mode `0755` so a restrictive root umask cannot leave them
non-executable. If an upgrade health check fails, the installer rolls back the
previous binaries and includes best-effort `systemctl status` / `journalctl`
output in the error.

`--tls-email` is rendered into Arivu-managed Caddy site blocks. Nginx and
Apache snippets still leave certificate ownership to the existing proxy.

`reconfigure` preloads the existing `/etc/arivu/arivu.env` domain, admin email,
bind port, proxy mode, TLS email, version, backup policy, and signup setting.
It does not replace the installed binary unless `--version`, `--artifact-url`,
or `--checksums-url` is passed. It does not force an admin password unless you
pass `--admin-password-file`, which rotates or creates the configured admin
account through `arivu admin bootstrap`. If backups are disabled in the
existing env file, pressing Enter at the backup prompt keeps them disabled.
On a root-managed reconfigure, turning backups off also disables the existing
`arivu-backup.timer` instead of leaving a previous timer running.

## Installed Files

- `/usr/local/bin/arivu`
- `/usr/local/bin/arivu-installer`
- `/etc/arivu/arivu.env`
- `/var/lib/arivu/arivu.sqlite3`
- `/var/backups/arivu/`
- `/etc/systemd/system/arivu.service`
- `/etc/systemd/system/arivu-backup.service`
- `/etc/systemd/system/arivu-backup.timer`

`/etc/arivu/arivu.env` is generated machine config. It should stay small:
listen address, SQLite path, public URL, secure-cookie default, signup default,
admin emails, and `SECRET_KEY`.

Routine settings should be changed in Admin > Settings. Runtime-editable values
include public URL, signup policy, secure-cookie status, model-provider, Resend,
and X settings. Secret provider values are encrypted in SQLite. Text generation
uses `AI_PROVIDER`, `AI_API_KEY`, `AI_MODEL`, and `AI_BASE_URL` or the matching
SQLite runtime settings `ai_provider`, `ai_api_key`, `ai_model`, and
`ai_base_url`. The selected provider supplies a sensible default Base URL that
admins can override. Legacy `GEMINI_API_KEY`, `GEMINI_MODEL`,
`GEMINI_BASE_URL`, and `gemini_*` runtime settings still backfill Gemini
deployments. Remote model-provider base URLs must use HTTPS unless they point to
localhost for development or tests.

Changing providers replaces the active provider tuple rather than carrying the
previous provider's credentials or model forward. Authenticated providers
require a new API Key during the switch. LM Studio, Ollama/local, and Custom can
run without a key; their requests omit the Authorization header unless a key is
configured. Provider requests do not follow HTTP redirects, so credentials stay
bound to the configured Base URL.

Built-in text-generation presets currently include OpenAI, OpenRouter, xAI,
Gemini, Anthropic, DeepSeek, Mistral, Groq, Together AI, Fireworks AI,
Perplexity, Cerebras, Z.ai, Hugging Face, LM Studio, Ollama/local, MiniMax, and
Custom. OpenAI-compatible providers use `/chat/completions`; Anthropic uses the
Messages API; Gemini uses the native Gemini generation endpoint. Provider-specific
embeddings and image OCR are intentionally deferred except for the existing
Gemini-backed paths. OpenCode-style client or proxy setups should use Custom.

## Backup, Restore, And Upgrade Safety

### Quality audit and safe repair

Audit is read-only and redacted: output contains aggregate counts, versions,
statuses, and reason codes, never URLs, titles, source text, authors, bookmark
IDs, or the full database path.

```bash
arivu quality audit --db /var/lib/arivu/arivu.sqlite3 --format json
arivu reprocess --db /var/lib/arivu/arivu.sqlite3 --stale-version --dry-run --user-id USER_ID
```

Apply requires explicit scope, a distinct integrity-checked installer backup
whose protected manual-data fingerprint matches the live database, and a batch
size from 1 to 100.

```bash
BACKUP_DIR=$(sudo arivu-installer backup)
sudo arivu reprocess \
  --db /var/lib/arivu/arivu.sqlite3 \
  --stale-version \
  --user-id USER_ID \
  --backup "$BACKUP_DIR" \
  --batch-size 25 \
  --apply
```

`--all-users` additionally requires `--confirm-all-users`. Repeating the same
command reuses the durable run and queues only the next untracked batch.
Queueing does not delete active summaries or semantics. Review a stratified
15-20 item sample and the acceptance report before continuing. Stop queueing if
quality gates fail; restore the verified installer backup for rollback.

Poll a queued run without opening SQLite directly:

```bash
arivu reprocess --db /path/to/arivu.sqlite3 --status RUN_ID
```

The redacted result reports durable run state, per-item status counts,
reason-code totals, and the last update time.

For older X-only databases, run X Sync before the repair batch. The sync
backfills authoritative API evidence for existing tweet IDs and queues those
bookmarks; reprocessing without that evidence finishes them as `partial`, never
as a false success. Library and Graph quarantine legacy semantic rows while the
backfill is incomplete.

`arivu-installer backup` fails if the primary SQLite database is missing and
uses a SQLite-consistent snapshot instead of raw-copying a live WAL database.

`arivu-installer restore` requires a primary `arivu.sqlite3` backup file. On a
root-managed install it stops only Arivu's service and backup timer, restores
through a temporary file, repairs ownership, starts Arivu again, and checks the
local `/api/health` endpoint before restarting the backup timer or reporting
restore success.

`arivu-installer upgrade` downloads the app and installer artifacts from the
same release and verifies both against `SHA256SUMS` before replacing anything.
If a custom application artifact is provided via `--artifact-url`, the upgrade
retains compatibility by skipping the companion installer binary download while
preserving atomic dual-binary safety properties. The upgrade mechanism runs as a
fully transactional process: both the `arivu` and `arivu-installer` destination
executables are prepared, tested, and atomically swapped. It preserves both previous
executables until the new app has passed systemd and local HTTP health checks.
Failed activation or health verification triggers an automatic rollback of both
executables to their previous states and restarts the previous Arivu service.
Embedded app-shell assets require browser cache revalidation, and service worker
registration/assets are revalidated while bypassing stale HTTP caching so the new
frontend is visible as soon as the upgraded service is healthy.

Installations created before installer self-updates were introduced need one
bootstrap refresh after upgrading to a release that includes this behavior:

```bash
curl -fsSL https://install.arivu.app | sudo bash
```

Because the existing env file is detected, this refresh upgrades in place and
does not rerun the setup wizard. Subsequent updates use
`sudo arivu-installer upgrade` directly.

## Manual Development Run

```bash
go run ./cmd/arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

Open `http://127.0.0.1:8080/auth`.

`go run` compiles a fixed binary snapshot. Stop and restart the process after
changing Go or embedded frontend source, switching branches, or pulling commits;
it does not hot reload the current checkout. SQLite-backed runtime settings are
resolved per request, so provider setting changes take effect without a restart.

For the repository's persistent local development database, load the ignored
environment file before starting the server:

```bash
set -a
source .env.local
set +a
GOCACHE=/private/tmp/arivu-build-cache go run ./cmd/arivu serve --addr 127.0.0.1:8080 --db "$ARIVU_DB"
```

In a second terminal, verify the restarted server before trying a capture:

```bash
curl -fsS http://127.0.0.1:8080/api/health
```

## Container

Docker remains an advanced/manual path.

```bash
docker build -t arivu:local .
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -v arivu-data:/data \
  -e ARIVU_ADDR=:8080 \
  -e ARIVU_DB=/data/arivu.sqlite3 \
  -e APP_URL=https://arivu.example.com \
  -e COOKIE_SECURE=true \
  -e SIGNUPS_ENABLED=false \
  -e ADMIN_EMAILS=admin@example.com \
  -e SECRET_KEY=replace-with-at-least-32-random-bytes \
  arivu:local
```

Compose sample:

```bash
cd deploy
docker compose up -d --build
```

## Manual systemd

Manual systemd remains available for debugging or unusual hosts. Prefer
`arivu-installer` for normal production installs.

```bash
go build -trimpath -ldflags="-s -w" -o arivu ./cmd/arivu
sudo install -m 0755 arivu /usr/local/bin/arivu
sudo useradd --system --home-dir /var/lib/arivu --create-home arivu
sudo install -d -o arivu -g arivu /var/lib/arivu /etc/arivu
sudo install -m 0640 -o root -g arivu deploy/arivu.env-sample /etc/arivu/arivu.env
sudo install -m 0644 deploy/arivu.service /etc/systemd/system/arivu.service
sudo systemctl daemon-reload
sudo systemctl enable --now arivu
```
