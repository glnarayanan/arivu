#!/usr/bin/env bash
# Cloud Agent install step for Arivu.
# Idempotent: safe to run repeatedly and against cached/snapshot state.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# go-sqlite3 needs cgo, so a C toolchain and CGO_ENABLED=1 are required.
export CGO_ENABLED=1

# Pin the Go toolchain to the version declared in go.mod.
required_go="$(awk '/^go [0-9]/ { print $2; exit }' go.mod)"
if [ -z "${required_go}" ]; then
  echo "install: could not read Go version from go.mod" >&2
  exit 1
fi

current_go=""
if command -v go >/dev/null 2>&1; then
  current_go="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
fi

if [ "${current_go}" != "${required_go}" ]; then
  echo "install: installing Go ${required_go} (found '${current_go:-none}')"
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' EXIT
  curl -fsSL -o "${tmp}/go.tar.gz" \
    "https://go.dev/dl/go${required_go}.linux-amd64.tar.gz"
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf "${tmp}/go.tar.gz"
  sudo ln -sf /usr/local/go/bin/go /usr/local/bin/go
  sudo ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  hash -r
fi

echo "install: using $(go version)"

# Prime module cache and warm the build cache so later builds/tests are fast.
go mod download
go build -trimpath -ldflags="-s -w" -o /tmp/arivu-install-build ./cmd/arivu
rm -f /tmp/arivu-install-build

echo "install: done"
