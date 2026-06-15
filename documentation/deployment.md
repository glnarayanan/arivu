# Deployment

Arivu can run as one process with a local SQLite database.

## Local

```bash
go run ./cmd/arivu serve -addr 127.0.0.1:8080 -db arivu.sqlite3
```

Open `http://127.0.0.1:8080/auth`.

## Environment

- `ARIVU_ADDR`: listen address, default `:8080`.
- `ARIVU_DB`: SQLite path, default `arivu.sqlite3`.
- `SECRET_KEY`: required outside development.
- `APP_URL`: public app URL used for emails and provider callbacks.
- `ADMIN_EMAILS`: comma-separated admin emails.
- `SIGNUPS_ENABLED`: defaults to `true`.
- `COOKIE_SECURE`: set `true` behind HTTPS.
- `GEMINI_API_KEY`, `RESEND_API_KEY`, `RESEND_FROM_EMAIL`, `X_*`: optional provider integrations.
- `X_API_BASE_URL` and `X_AUTHORIZE_URL`: optional X endpoint overrides for tests or controlled environments; production should use defaults.

## Production Notes

- Run behind TLS and set `COOKIE_SECURE=true`.
- Back up the SQLite database and WAL files together.
- Keep `ARIVU_ADDR` bound to loopback unless your firewall and proxy topology require otherwise.
- FTS5 is preferred but optional; the app supports the built-in `LIKE` search fallback.
- Keep the legacy deployment available until migration validation passes.

## Container

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

## systemd

```bash
go build -trimpath -ldflags="-s -w" -o arivu ./cmd/arivu
sudo install -m 0755 arivu /usr/local/bin/arivu
sudo useradd --system --home-dir /var/lib/arivu --create-home arivu
sudo install -d -o arivu -g arivu /var/lib/arivu /etc/arivu
sudo install -m 0640 -o root -g arivu deploy/arivu.env-sample /etc/arivu/arivu.env
sudo install -m 0644 deploy/arivu.service /etc/systemd/system/arivu.service
```

Edit `/etc/arivu/arivu.env`, then start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now arivu
sudo systemctl status arivu
```
