# Deployment

Asset reconciliation runs at startup and hourly. Set `ARIVU_ASSET_GC_GRACE`
(default `24h`) to control the minimum age before stale staging or unreferenced
objects are removed; referenced objects are never garbage-collected, and missing
referenced content is reported in server logs without changing SQLite metadata.

## Complete browser capture

Fresh installer runs enable the isolated `capture/` service by default, with an
explicit opt-out for core-only hosts. The Go binary's bounded direct-HTTP path
remains the independent fallback and the complete runtime is the only production
bundle that contains Node, Playwright/Chromium, Mozilla Readability, JSDOM, and
Monolith.

Capture is fully background and headless. It never opens a tab in the user's
browser, never reads a browser profile, and never receives the user's cookies.
For every attempt Arivu creates an authenticated Unix-socket egress proxy;
Chromium and Monolith can reach the network only through that proxy. The Go
process performs public-address validation, DNS-rebinding protection, redirect
checks, response limits, and aggregate byte accounting.

Protocol v2 returns rendered reader HTML/text, metadata, component status,
bounded artifacts, and bounded reader images over one Unix socket. Arivu
validates the entire manifest before ingesting any payload. Media and evidence
activate as one staged batch, so a failed retry cannot leave half-updated reader
HTML or replace the last good copy. Self-contained HTML downloads remain raw;
the in-app preview is separately transformed inert, placed in a scriptless
sandbox, and limited to 8 MiB to bound DOM parsing memory.

Fresh installer runs enable screenshot and self-contained HTML preservation by
default; PDF stays off because of its storage cost. The installer asks before
applying the plan, and `--browser-capture=false` keeps the pure Go direct-capture
path. Complete capture supports Ubuntu 22.04+ and Debian 12+ on AMD64 and ARM64;
older supported core-only hosts must opt out. Existing installations retain
their current setting during reconfigure.

No Docker, npm, Node, Chromium, Rust, or Monolith setup is required from the
operator. Container hosts use the two-service stack described under
[Compose / PaaS](#compose--paas-docker) instead. Tagged releases contain a
checksummed, architecture-specific `arivu-capture-linux-<arch>.tar.gz` with the
pinned runtime. `arivu-installer`:

1. Streams and verifies the archive against the release `SHA256SUMS`.
2. Rejects unsafe archive paths, links, unsupported entries, and size excesses.
3. Installs only Chromium's required host libraries through the bundled,
   approved Playwright runtime.
4. Launch-checks Chromium and verifies Monolith 2.11.6 as the unprivileged
   `arivu-capture` user.
5. Atomically activates `/usr/local/lib/arivu-capture`, writes the hardened
   systemd unit, starts capture before Arivu, and checks both services.

`sudo arivu-installer upgrade` updates the app, installer, and enabled capture
runtime from the same release. Activation failure restores all prior artifacts
and restarts the previous services.

`/usr/local/lib/arivu-capture` belongs to the installer and is replaced as a
whole on every install and upgrade, so keep local helpers for systemd drop-ins
in `/etc/systemd/system/arivu-capture.service.d/` somewhere else, such as
`/usr/local/libexec`. If a drop-in refers to a file inside the runtime
directory that the new release does not ship, the installer stops before
replacing anything and names the drop-in.

Use reconfigure to change the choice:

```bash
sudo arivu-installer reconfigure                  # interactive
sudo arivu-installer reconfigure --browser-capture=false
sudo arivu-installer reconfigure --browser-capture=true
sudo arivu-installer status
```

Disabling capture removes its systemd unit and bundled runtime but preserves
existing reader content and saved artifacts. The default direct reader remains
available independently, and browser or Monolith failure becomes a partial
capture rather than discarding successful evidence.

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
- Complete browser capture policy.
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
untagged local builds. Releases also publish a native capture runtime for each
supported architecture. Install and upgrade write app and installer binaries with
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
- `/usr/local/lib/arivu-capture/` when complete capture is enabled
- `/etc/arivu/arivu.env`
- `/var/lib/arivu/arivu.sqlite3`
- `/var/backups/arivu/`
- `/etc/systemd/system/arivu.service`
- `/etc/systemd/system/arivu-capture.service` when enabled
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
Messages API; Gemini uses the native Gemini generation endpoint. Image OCR is
intentionally deferred except for the existing Gemini-backed path. OpenCode-style
client or proxy setups should use Custom.

### Embedding model

Embeddings are configured separately from text generation, under **Embedding
model** in Admin > Settings (and Settings > Provider settings). They power
semantic ranking in Graph search, related items, Graph similarity links, and
similar-content duplicate detection. Without them those features fall back to
keywords and extracted topics, and Settings says so.

| Setting | Environment | Meaning |
| --- | --- | --- |
| `embedding_provider` | `EMBEDDING_PROVIDER` | `same` (default), a preset id, or `none` |
| `embedding_api_key` | `EMBEDDING_API_KEY` | Encrypted with `SECRET_KEY`; only shown masked |
| `embedding_model` | `EMBEDDING_MODEL` | Empty uses the provider's default embedding model |
| `embedding_base_url` | `EMBEDDING_BASE_URL` | Empty uses the preset; HTTPS unless localhost |
| `embedding_dimensions` | `EMBEDDING_DIMENSIONS` | Optional; only for APIs that let the caller choose |

`same` reuses the text generation provider, key, and Base URL when that
provider offers embeddings, so the common case needs no extra key. If the text
provider is text-only (for example DeepSeek), `same` leaves embeddings
unconfigured and Settings explains why; pick an embedding provider instead. A
separately chosen provider never borrows the text generation key, and switching
embedding providers clears the previous provider's key, model, Base URL, and
dimensions stored in SQLite. `EMBEDDING_MODEL`, `EMBEDDING_BASE_URL`, and
`EMBEDDING_DIMENSIONS` from the environment remain fallbacks for whichever
provider is selected, so keep them empty when switching providers in the UI.

Embedding support per preset, checked against each provider's public API docs
in October 2026:

| Preset | Embeddings | Default model | Dimensions |
| --- | --- | --- | --- |
| Gemini | Native `embedContent` | `gemini-embedding-2` | 768 by default (`output_dimensionality`, 128-3072) |
| OpenAI | `POST /embeddings` | `text-embedding-3-small` | `dimensions` |
| OpenRouter | `POST /embeddings` | `openai/text-embedding-3-small` | `dimensions` |
| Mistral | `POST /embeddings` | `mistral-embed` (fixed 1024) | `output_dimension` (codestral-embed) |
| Fireworks AI | `POST /embeddings` | `nomic-ai/nomic-embed-text-v1.5` | `dimensions` |
| Perplexity | `POST /v1/embeddings`, base64 int8 vectors | `pplx-embed-v1-0.6b` | `dimensions` |
| Ollama/local | `POST /v1/embeddings` | `embeddinggemma` | `dimensions` |
| LM Studio | `POST /v1/embeddings` | none; enter the loaded model id | not offered |
| Custom | Any OpenAI-compatible `POST {base}/embeddings` | none | `dimensions`, sent only when set |
| xAI, Anthropic, DeepSeek, Groq, Cerebras, Z.ai, MiniMax | Text only | | |
| Together AI | Text only: no serverless embedding models; use Custom with a dedicated endpoint | | |
| Hugging Face | Text only: the router's OpenAI-compatible API is chat-only; use Custom with an OpenAI-compatible embeddings endpoint | | |

Each stored vector records its space, `provider/model@dimensions`, in
`bookmarks.embedding_model`. Vectors from different spaces are never compared.
When the embedding provider, model, or dimensions change, older vectors are
excluded from similarity at once and rebuilt by the `embedding.reembed`
background job: batches of 16 at low priority on the single job worker, resumed
after restarts, scheduled at startup, hourly, after settings changes, and from
**Re-embed now**. An item the provider rejects (400/413/422) is skipped and
retried by a later pass; a 404, 405, or 409, or three rejections without a
single success, pauses the pass as a failed job that names the likely cause (a
wrong model id or Base URL). Admin > API Usage and the Embedding model section show
embedded, stale, and missing counts. A missing embedding model is reported as
`embedding_not_configured`, never as the text provider's
`provider_not_configured`.

Upgrading from 1.8.3 or earlier labels existing vectors as
`gemini/gemini-embedding-2@768`. Gemini deployments keep them. With any other
effective embedding model, the first start re-embeds existing items (and embeds
items that never got a vector) in the background, using that provider's quota.

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

## Compose / PaaS (Docker)

The installer stays the primary path for a Linux host you manage by hand. For a
container host or a self-hosted PaaS such as Dokploy, the root `compose.yaml`
runs Arivu as two services with complete capture. Both paths keep the same data
format, so an installer backup restores into the container stack (see below),
but a running installer host and a running stack never share data.

### What runs

| Service | Image | Runs as | Network | Writes to |
| --- | --- | --- | --- | --- |
| `arivu` | root `Dockerfile` | uid 10001, group `arivu` (10001) | compose network; no published host port | `/data` volume, `/run/arivu-capture` socket volume, `/tmp` (tmpfs) |
| `capture` | `capture/Dockerfile` | uid 10002, group `arivu` (10001) | `network_mode: none` | `/run/arivu-capture` socket volume, `/tmp` (tmpfs) |

The capture image carries the same pinned runtime as the native release
archive: Node 24.22.1, the Chromium build pinned by Playwright in
`capture/package-lock.json`, and Monolith 2.11.6 built with Rust 1.99.1. It
starts the helper as the systemd unit does (`node src/index.mjs` in
`/usr/local/lib/arivu-capture`), after creating the shared
`/run/arivu-capture/attempts` directory with mode `0770`.

The app listens on `:8080` inside its container. A reverse proxy on the same
Docker network (Traefik under Dokploy) terminates TLS and forwards to
`arivu:8080`. As behind Caddy on an installer host, the app sees the proxy as
the client address.

### Isolation compared with the systemd units

| systemd (`arivu-capture.service`) | Compose (`capture`) |
| --- | --- |
| `PrivateNetwork=true` | `network_mode: none` (loopback only; the web is reachable only through the per-attempt proxy socket the app creates) |
| `User=arivu-capture`, `Group=arivu` | uid 10002, gid 10001; the app is uid 10001, gid 10001 |
| `RuntimeDirectory=arivu-capture`, mode `0770` | `capture-socket` tmpfs volume owned by 10002:10001, mode `0770` |
| `CapabilityBoundingSet=` (empty) | `cap_drop: [ALL]` |
| `NoNewPrivileges=true` | `no-new-privileges:true` |
| `ProtectSystem=strict`, `ReadWritePaths=/run/arivu-capture` | `read_only: true` plus the socket volume |
| `PrivateTmp=true` | private tmpfs on `/tmp` (1 GiB by default) |
| `TasksMax=512` | `pids_limit: 512` |
| no memory cap | `mem_limit` and `memswap_limit` of 1536 MiB by default |
| `LimitNOFILE=65536` | the Docker daemon default, so the stack starts on hosts with a lower hard limit |
| `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6` | seccomp allows `socket` for the same three families only |
| Chromium runs with `--no-sandbox` | Chromium's sandbox is on (see below) |

The app container keeps Docker's default seccomp and AppArmor profiles, is
read-only, drops every capability, and sets `no-new-privileges`. The capture
container uses the two profiles in `capture/security`, each Docker's default
plus only what Chromium's sandbox needs.

The socket modes match the native install: the helper's socket is `0660`, each
attempt directory the app creates is `0750` (the helper can enter it but not
replace anything), and each per-attempt proxy socket is `0660`.

### Chromium's own sandbox

Chromium's sandbox is on in the capture container (since 1.8.3). Each renderer,
the process that parses and runs a page's HTML and JavaScript, starts in its
own user, pid, and network namespaces, chrooted into an empty directory, under
Chromium's seccomp-bpf policy. The container boundary around it is unchanged.

**Why it matters.** Before 1.8.3 the renderer ran as the helper's own user in
the helper's own namespaces. A page that exploited a renderer bug could then
connect to the helper's socket and to the per-attempt proxy sockets, and read
or alter other captures in flight, while still being stopped by the container
(no network, no capabilities, read-only root, no data or secrets). Now the
same exploit lands in an empty chroot (no socket paths to reach), in its own
user, pid, and network namespaces, under Chromium's seccomp-bpf policy, which
allows only the small set of syscalls a renderer needs. It needs a second bug,
a Chromium sandbox escape, before it can reach the helper or its sockets, and
a third, a container escape, before it can reach the host.

**How, without loosening the container.** Docker's default seccomp profile
allows namespace creation only with `CAP_SYS_ADMIN`, and Ubuntu's AppArmor
restriction (`kernel.apparmor_restrict_unprivileged_userns=1`) refuses user
namespaces unless the confining profile allows them. The capture service
therefore runs with:

- `seccomp=./capture/security/seccomp-chromium.json`: Docker's default plus
  `clone` creating user, pid, and network namespaces (never mount, cgroup,
  UTS, or IPC), `unshare` creating only a user namespace, and `chroot`
  (which still needs `CAP_SYS_CHROOT`, so it works only inside such a
  namespace). `socket` is limited to AF_UNIX, AF_INET, and AF_INET6, like the
  native unit, so a process holding `CAP_NET_ADMIN` in its own network
  namespace still cannot reach netfilter or rtnetlink.
- `apparmor=arivu-capture`: `docker-default` plus `userns` (and an explicit
  `unix` rule, which ABI 4.0 needs).

No capability is added; `no-new-privileges`, `cap_drop: [ALL]`, `network_mode:
none`, `read_only`, uid 10002, and the limits all stay; nothing is
`unconfined` or privileged, and no setuid helper is used.
`capture/security/README.md` lists every rule and why.

**Remaining trade-off.** The helper's own processes (Node, Chromium's browser
and network-service processes, Monolith) can now create user, pid, and network
namespaces, which exposes kernel code that Docker's default keeps out of
reach. Only code that already runs outside Chromium's sandbox can use it, and
the socket limit removes netlink, the usual route to user-namespace kernel
bugs. Keep the host kernel patched.

#### Host prerequisite: install the AppArmor profile

On a host with AppArmor (Ubuntu, Debian), install the profile **before**
deploying, and again whenever a release changes
`capture/security/apparmor-arivu-capture`. Docker refuses to start the capture
container while the named profile is not loaded, and when that happens during
an upgrade, Compose also leaves the new app container stopped (see Recovery
below).

```bash
TAG=v1.8.3   # the tag you deploy
curl -fsSL -o /tmp/apparmor-arivu-capture \
  "https://raw.githubusercontent.com/glnarayanan/arivu/$TAG/capture/security/apparmor-arivu-capture"
sudo install -m 0644 -o root -g root /tmp/apparmor-arivu-capture /etc/apparmor.d/arivu-capture
sudo apparmor_parser -r -W /etc/apparmor.d/arivu-capture
sudo grep '^arivu-capture ' /sys/kernel/security/apparmor/profiles   # arivu-capture (enforce)
```

`-r` loads or replaces the profile and `-W` writes its cache. Files in
`/etc/apparmor.d` load again at boot, so the profile survives reboots. Check
for AppArmor with `cat /sys/module/apparmor/parameters/enabled` (`Y`).

**Hosts without AppArmor** need nothing: Docker ignores the `apparmor=` option
there, and the seccomp profile alone lets the sandbox start, so plain `docker
compose up` works. `ARIVU_CAPTURE_APPARMOR_PROFILE` names a differently named
profile you loaded yourself; never set it to `unconfined`.

#### Verify

```bash
docker compose -p <app-name> logs capture | grep 'capture helper ready'
# capture helper ready: Chromium 156.0.8078.4, sandbox active, Monolith 2.11.6
docker compose -p <app-name> exec -T capture node src/preflight.mjs
# {"chromium":"…","chromium_sandbox":"active","renderers":[{"user_namespace":true,"pid_namespace":true,"network_namespace":true,"chroot":true,"seccomp_bpf":true}],"monolith":"2.11.6"}
docker compose -p <app-name> exec -T capture cat /proc/1/attr/current
# arivu-capture (enforce)          (AppArmor hosts only)
```

`preflight.mjs` launches a browser, opens a page, and reports `active` only
after it has found the renderer in its own user, pid, and network namespaces,
chrooted, with one more seccomp filter than the browser process.

#### Failure behavior

The helper never falls back to `--no-sandbox`.

- If the sandbox cannot start, the helper exits before opening its socket and
  logs one line, for example `capture helper preflight failed: capture
  Chromium could not start its sandbox (ARIVU_CAPTURE_CHROMIUM_SANDBOX=true).
  The capture container needs the seccomp profile …`. Docker keeps restarting
  it; Arivu keeps saving through direct HTTP and records
  `browser_helper_unavailable` for each attempt.
- If a single capture's renderer is not sandboxed, or its browser cannot
  start the sandbox, that capture fails with `browser_sandbox_unavailable`,
  the helper logs why, and Arivu keeps the direct-HTTP copy.

#### Recovery and opting out

- **Profile missing after an upgrade** (capture keeps failing to start, app
  stopped in `created`): install the profile as above and run the deployment
  again (`docker compose -p <app-name> up -d`).
- **Run without Chromium's sandbox** (the pre-1.8.3 posture, for a host where
  the profiles cannot be used): set `ARIVU_CAPTURE_CHROMIUM_SANDBOX=false` and
  `ARIVU_CAPTURE_APPARMOR_PROFILE=docker-default`. The container boundary
  stays as described above.

#### Native installs

Unchanged. `arivu-capture.service` does not set
`ARIVU_CAPTURE_CHROMIUM_SANDBOX`, so the helper still launches Chromium with
`--no-sandbox` inside the unit's isolation (`PrivateNetwork`, empty capability
set, `RestrictAddressFamilies`, and the rest). Turning it on there would need
an AppArmor profile for the bundled Chromium on Ubuntu 24.04 and later, so it
is left for a separate change.

### Settings

Set these in the PaaS environment or in a `.env` file next to `compose.yaml`.
Compose refuses to start while a required one is missing.

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `APP_URL` | yes | | Public `https://` URL. |
| `ADMIN_EMAILS` | yes | | Comma-separated admin emails. |
| `SECRET_KEY` | yes | | Encrypts provider secrets in SQLite. When moving an existing install, reuse the value from `/etc/arivu/arivu.env`; a new key cannot read secrets saved under the old one. |
| `COOKIE_SECURE` | no | `true` | Keep `true` behind HTTPS. |
| `SIGNUPS_ENABLED` | no | `false` | Open signup. |
| `ARIVU_FETCH_USER_AGENT` | no | `Arivu/2.0` | Outbound user agent. |
| `ARIVU_BROWSER_CAPTURE_ENABLED` | no | `true` | `false` keeps the direct-HTTP path only. |
| `ARIVU_BROWSER_CAPTURE_SCREENSHOT` / `_SELF_CONTAINED_HTML` / `_PDF` | no | `true` / `true` / `false` | Preservation formats, as the installer sets them. |
| `ARIVU_BROWSER_CAPTURE_TIMEOUT`, `_NAVIGATION_TIMEOUT`, `_MAX_*` | no | installer values | Capture limits; see `compose.yaml`. |
| `ARIVU_CAPTURE_MEM_LIMIT` | no | `1536m` | Capture container memory cap (no swap). |
| `ARIVU_CAPTURE_TMP_SIZE` | no | `1g` | Capture `/tmp` tmpfs size; counts toward its memory. |
| `ARIVU_CAPTURE_CHROMIUM_SANDBOX` | no | `true` | Chromium's own sandbox in the capture container. `false` only with `ARIVU_CAPTURE_APPARMOR_PROFILE=docker-default`; see above. |
| `ARIVU_CAPTURE_APPARMOR_PROFILE` | no | `arivu-capture` | AppArmor profile for the capture container; must be loaded on AppArmor hosts. Ignored on hosts without AppArmor. Never `unconfined`. |
| `ARIVU_VERSION` | no | empty | Tag reported by `arivu --version` (for example `v1.8.3`). |
| `ARIVU_IMAGE_TAG` | no | `local` | Tag for the two built images. |

The socket path, runtime directory, and protocol are fixed in `compose.yaml`
because both containers must agree on them.

### Dokploy

1. On the Dokploy host, install the capture AppArmor profile for the tag you
   will deploy ([Host prerequisite](#host-prerequisite-install-the-apparmor-profile)).
   Dokploy hosts are usually Ubuntu, where the capture container does not
   start without it.
2. Create a **Compose** service from `glnarayanan/arivu` with compose path
   `./compose.yaml`. Deploy from a release tag or from `main`.
3. Set the environment from the table above, and set `ARIVU_VERSION` to the
   tag you deploy.
4. Add the domain to service `arivu`, container port `8080`. Dokploy attaches
   its `dokploy-network` to that service. `compose.yaml` lists the app's
   networks directly, not through a YAML anchor, because Dokploy rewrites that
   key. Leave `capture` without a domain: it has `network_mode: none`.
5. Deploy and wait until both services report healthy. The app's health check
   runs `arivu healthcheck` (a request to `/api/health`); the helper's health
   check connects to its socket. Then [verify](#verify) that the helper
   reports `sandbox active`.
6. For a new instance, create the first admin:

   ```bash
   docker compose -p <app-name> exec -T arivu arivu admin bootstrap \
     --email admin@example.com --password-stdin < admin-password.txt
   ```

7. Under **Volume Backups**, back up the volume ending in `arivu-data` and let
   Dokploy stop service `arivu` while it copies, so the SQLite files are
   consistent. The `capture-socket` volume holds only sockets and never needs a
   backup.

Run the commands below from the directory that holds the deployed
`compose.yaml` and its `.env` (Dokploy keeps them under
`/etc/dokploy/compose/<app-name>/code`), with `-p <app-name>`.

### Plain Docker Compose

```bash
cp .env.example .env   # or write APP_URL, ADMIN_EMAILS, SECRET_KEY yourself
# AppArmor hosts only, once per profile change:
sudo install -m 0644 capture/security/apparmor-arivu-capture /etc/apparmor.d/arivu-capture
sudo apparmor_parser -r -W /etc/apparmor.d/arivu-capture
docker compose up -d --build
```

Nothing is published on the host. To reach the app without a proxy on the
compose network, add a `compose.override.yaml` next to `compose.yaml`:

```yaml
services:
  arivu:
    ports:
      - "127.0.0.1:8080:8080"
```

### Volumes and backups

| Volume | Mounted at | Holds | Back up |
| --- | --- | --- | --- |
| `arivu-data` | `/data` (app) | `arivu.sqlite3` and its WAL, `arivu.sqlite3.assets/`, `backups/` from `arivu backup` | yes |
| `capture-socket` | `/run/arivu-capture` (both) | sockets and per-attempt directories; an 8 MiB tmpfs that starts empty | no |

`arivu backup` writes the same verified format as `arivu-installer backup`: a
SQLite-consistent snapshot, the asset directory, and a size and SHA-256
manifest. It is safe while the app runs and prints the new directory under
`/data/backups`:

```bash
docker compose -p <app-name> exec -T arivu arivu backup
```

The container stack has no backup timer. Run that command from a scheduler if
you want daily snapshots in addition to the volume backup, and prune old ones,
for example
`docker compose -p <app-name> exec -T arivu find /data/backups -mindepth 1 -maxdepth 1 -mtime +14 -exec rm -rf {} +`.

### Admin commands

Read-only commands and commands that are safe beside the server use `exec`:

```bash
docker compose -p <app-name> exec -T arivu arivu --version
docker compose -p <app-name> exec -T arivu arivu quality audit --format json
docker compose -p <app-name> exec -T arivu arivu reprocess --stale-version --dry-run --user-id USER_ID
```

`arivu reprocess --apply` requires a distinct, integrity-checked backup whose
protected data matches the live database. In the container, take it with
`arivu backup` immediately before applying:

```bash
docker compose -p <app-name> exec -T arivu sh -c '
  BACKUP_DIR=$(arivu backup) &&
  arivu reprocess --stale-version --user-id USER_ID \
    --backup "$BACKUP_DIR" --batch-size 25 --apply'
```

`ARIVU_DB` is already `/data/arivu.sqlite3` inside the container, so `--db` is
not needed.

### Move an installer instance into the stack

1. On the installer host, stop the app so no save lands after the snapshot,
   take a backup, and package it:

   ```bash
   sudo systemctl stop arivu
   BACKUP_DIR=$(sudo arivu-installer backup)
   sudo tar -C "$(dirname "$BACKUP_DIR")" -czf ~/arivu-backup.tgz "$(basename "$BACKUP_DIR")"
   sudo grep '^SECRET_KEY=' /etc/arivu/arivu.env
   ```

   Start it again with `sudo systemctl start arivu` if it should keep serving
   until the cutover.
2. Deploy the stack with the same `SECRET_KEY` and wait until it is healthy.
3. Copy `arivu-backup.tgz` to the container host, unpack it, and give it to the
   container user. Installer backups are owned by root with mode `0640`, which
   uid 10001 cannot read:

   ```bash
   mkdir -p /root/arivu-restore
   tar -C /root/arivu-restore -xzf arivu-backup.tgz
   chown -R 10001:10001 /root/arivu-restore/<timestamp>
   ```

4. Stop the app, restore into the data volume as the app user, and start it:

   ```bash
   docker compose -p <app-name> stop arivu
   docker compose -p <app-name> run --rm --no-deps \
     -v /root/arivu-restore/<timestamp>:/restore:ro arivu restore --backup /restore
   docker compose -p <app-name> start arivu
   ```

`arivu restore` verifies the manifest and every stored object before it
touches `/data`, swaps the database and assets, checks the restored database,
and puts the previous files back if anything fails. Files it writes belong to
uid 10001, so no ownership repair is needed. It refuses to run while the server
has the database open. The same command restores a backup made by
`arivu backup`.

### What has been tested

Tested locally with Docker 29 and Compose 5 on a Linux host without AppArmor:
both images build; the stack starts healthy with plain `docker compose`;
`capture` has only a loopback interface, no capabilities, and no DNS or
outbound connection; a save through the app produced a rendered reader copy,
a JPEG screenshot, and a self-contained HTML archive through the per-attempt
proxy socket; an `arivu-installer backup` archive restored into a fresh stack
with the documented commands, and restore refused while the app was running;
`arivu reprocess --apply` accepted an `arivu backup`; and the app stayed
reachable with an external `dokploy-network` attached. That test host
re-signed outbound HTTPS, which Chromium rightly rejected, so the end-to-end
browser captures used plain-HTTP pages.

Chromium's sandbox (1.8.3) was tested the same way on Docker 29.9.2 without
AppArmor: under Docker's default seccomp profile the sandbox refuses to start
and the helper exits with its one-line reason while the app keeps saving
through direct HTTP; under `seccomp-chromium.json` the preflight reports
`active` with every renderer check true, and saves through the app (two of
them at once) produced a JPEG screenshot and a self-contained HTML archive;
`unshare` of anything but a user namespace, `chroot` outside one, and netlink
sockets are refused. The AppArmor profile was syntax-checked with
`apparmor_parser` 4.1 but not enforced.

Not tested: a real Dokploy deployment, Traefik and Cloudflare in front,
Dokploy's volume backups, an Ubuntu 26.04 host with AppArmor (including the
`arivu-capture` profile under enforcement), ARM64 images, and HTTPS captures
on a host without TLS interception.

### Single container

The app image alone runs without complete capture:

```bash
docker build -t arivu:local .
docker run --rm \
  -p 127.0.0.1:8080:8080 \
  -v arivu-data:/data \
  -e APP_URL=https://arivu.example.com \
  -e ADMIN_EMAILS=admin@example.com \
  -e SECRET_KEY=replace-with-at-least-32-random-bytes \
  arivu:local
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
