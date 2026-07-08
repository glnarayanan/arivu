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

The bootstrap script only downloads `arivu-installer`, verifies it against the
release `SHA256SUMS`, verifies the GitHub artifact attestation with `gh`, installs
it under `/usr/local/bin`, and starts the interactive installer. Hosts must have
`gh` available for the one-command path; this keeps checksum verification from
being the only trust control.

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
sudo arivu-installer backup
sudo arivu-installer restore --backup /var/backups/arivu/20260708T010203Z
sudo arivu-installer upgrade
sudo arivu-installer reconfigure
sudo arivu-installer uninstall
```

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
include public URL, signup policy, secure-cookie status, Gemini, Resend, and X
settings. Secret provider values are encrypted in SQLite.

## Backup, Restore, And Upgrade Safety

`arivu-installer backup` fails if the primary SQLite database is missing and
uses a SQLite-consistent snapshot instead of raw-copying a live WAL database.

`arivu-installer restore` requires a primary `arivu.sqlite3` backup file. On a
root-managed install it stops only Arivu's service and backup timer, restores
through a temporary file, repairs ownership, starts Arivu again, and checks the
local `/api/health` endpoint before restarting the backup timer or reporting
restore success.

`arivu-installer upgrade` keeps the previous binary until the replacement has
passed systemd and local HTTP health checks. Failed health checks roll the
binary back and restart the Arivu service.

## Manual Development Run

```bash
go run ./cmd/arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

Open `http://127.0.0.1:8080/auth`.

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
