#!/usr/bin/env bash
# Cloud Agent install step for Arivu.
# Idempotent: safe to run repeatedly and against cached/snapshot state.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# go-sqlite3 requires cgo; a C toolchain ships in the base image.
export CGO_ENABLED=1

# The Go version is pinned by the `go` directive in go.mod. The base image's Go
# honors GOTOOLCHAIN=auto and transparently fetches the exact pinned toolchain,
# so every `go` command below runs under that version. Downloading the modules
# (and, on first run, the toolchain itself) bakes them into the environment so
# later builds and tests run without network access.
go mod download

# Warm the build cache and confirm the cgo build links cleanly.
go build -trimpath -ldflags="-s -w" -o /tmp/arivu-install-build ./cmd/arivu
rm -f /tmp/arivu-install-build

echo "install: prepared $(go version) with cached modules"
