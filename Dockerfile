# syntax=docker/dockerfile:1

FROM golang:1.27.3-trixie AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Release tag reported by `arivu --version`; empty builds report "devel".
ARG ARIVU_VERSION=
RUN CGO_ENABLED=1 go build -trimpath \
  -ldflags="-s -w -X github.com/glnarayanan/arivu/internal/buildinfo.releaseVersion=${ARIVU_VERSION}" \
  -o /out/arivu ./cmd/arivu

FROM debian:trixie-slim AS runtime

# Upgrade first so the image picks up Debian security fixes published after
# the base image was built (the trixie-slim tag is not rebuilt for each one).
RUN export DEBIAN_FRONTEND=noninteractive \
  && apt-get update \
  && apt-get upgrade -y --no-install-recommends \
  && apt-get install -y --no-install-recommends ca-certificates tzdata \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --system --gid 10001 arivu \
  && useradd --system --uid 10001 --gid arivu --home-dir /var/lib/arivu --create-home arivu \
  && install -d -o arivu -g arivu /data

COPY --from=build /out/arivu /usr/local/bin/arivu

USER arivu
WORKDIR /var/lib/arivu
ENV ARIVU_ADDR=:8080
ENV ARIVU_DB=/data/arivu.sqlite3
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["/usr/local/bin/arivu", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/arivu"]
CMD ["serve"]
