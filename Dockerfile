# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/arivu ./cmd/arivu

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
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

ENTRYPOINT ["/usr/local/bin/arivu"]
CMD ["serve"]
