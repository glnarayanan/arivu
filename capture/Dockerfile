# syntax=docker/dockerfile:1

# Container image for the isolated browser-capture helper. It ships the same
# pinned runtime as the native release archive (.github/workflows/release.yml):
# Node 24.22.1, the Chromium build pinned by Playwright in package-lock.json,
# and Monolith 2.11.6 built with Rust 1.99.1. Build context: this directory.
#
# The root compose.yaml runs it with no network, a read-only root filesystem,
# no capabilities, a shared Unix-socket volume, and the seccomp and AppArmor
# profiles in capture/security that let Chromium's own sandbox run. See
# openwiki/operations/deployment.md ("Compose / PaaS").

ARG NODE_IMAGE=node:24.22.1-trixie-slim
ARG RUST_IMAGE=rust:1.99.1-trixie

FROM ${RUST_IMAGE} AS monolith
RUN cargo install --locked --version 2.11.6 --root /opt/monolith monolith

FROM ${NODE_IMAGE} AS deps
WORKDIR /usr/local/lib/arivu-capture
ENV PLAYWRIGHT_BROWSERS_PATH=/usr/local/lib/arivu-capture/browsers
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund \
  && node node_modules/playwright/cli.js install chromium \
  && rm -rf node_modules/.bin \
  && npm cache clean --force

FROM ${NODE_IMAGE} AS runtime
WORKDIR /usr/local/lib/arivu-capture
# Chromium's own sandbox is on in the container. It needs the seccomp and
# AppArmor profiles in capture/security (compose.yaml applies them); without
# them the helper refuses to start rather than run Chromium unsandboxed.
ENV NODE_ENV=production \
  ARIVU_CAPTURE_CHROMIUM_SANDBOX=true \
  PLAYWRIGHT_BROWSERS_PATH=/usr/local/lib/arivu-capture/browsers \
  ARIVU_MONOLITH_PATH=/usr/local/lib/arivu-capture/monolith \
  ARIVU_CAPTURE_SOCKET=/run/arivu-capture/helper.sock \
  ARIVU_CAPTURE_RUNTIME_DIR=/run/arivu-capture/attempts \
  HOME=/tmp

# Chromium's shared libraries come from the bundled Playwright, as on native
# installs. ca-certificates lets Monolith verify TLS through the capture proxy.
COPY --from=deps /usr/local/lib/arivu-capture/node_modules ./node_modules
RUN node node_modules/playwright/cli.js install-deps chromium \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --system --gid 10001 arivu \
  && useradd --system --uid 10002 --gid arivu --no-create-home --home-dir /nonexistent \
    --shell /usr/sbin/nologin arivu-capture

COPY --from=deps /usr/local/lib/arivu-capture/browsers ./browsers
COPY --from=monolith /opt/monolith/bin/monolith ./monolith
COPY package.json package-lock.json README.md ./
COPY src ./src
COPY docker-entrypoint.sh /usr/local/bin/arivu-capture-entrypoint

# The app (uid 10001, group arivu) and this helper (uid 10002, group arivu)
# share /run/arivu-capture. The helper's socket is mode 0660; the app's
# per-attempt directories are 0750 and its proxy sockets 0660.
USER arivu-capture:arivu
ENTRYPOINT ["/usr/local/bin/arivu-capture-entrypoint"]
CMD ["node", "src/index.mjs"]
