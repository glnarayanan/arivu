# syntax=docker/dockerfile:1

FROM rust:1.88.0-bookworm AS monolith

RUN cargo install --locked --version 2.10.1 --root /opt/monolith monolith

FROM node:22.23.1-bookworm-slim

ENV NODE_ENV=production
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
ENV ARIVU_CAPTURE_SOCKET=/run/arivu-capture/helper.sock
ENV ARIVU_CAPTURE_RUNTIME_DIR=/run/arivu-capture/attempts
ENV ARIVU_MONOLITH_PATH=/usr/local/bin/monolith

WORKDIR /usr/local/lib/arivu-capture
COPY package.json package-lock.json ./
RUN npm ci --omit=dev \
  && npx playwright install --with-deps chromium \
  && npm cache clean --force \
  && groupadd --system --gid 10001 arivu \
  && useradd --system --uid 10002 --gid arivu --home-dir /var/lib/arivu-capture --create-home arivu-capture \
  && install -d -m 0770 -o arivu-capture -g arivu /run/arivu-capture /run/arivu-capture/attempts \
  && chown -R arivu-capture:arivu /ms-playwright /usr/local/lib/arivu-capture

COPY --from=monolith /opt/monolith/bin/monolith /usr/local/bin/monolith
COPY src ./src

USER arivu-capture
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 CMD ["node", "src/health.mjs"]
ENTRYPOINT ["node", "src/index.mjs"]
