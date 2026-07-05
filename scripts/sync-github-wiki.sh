#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="${1:-$root/tmp/wiki}"

rm -rf "$out"
mkdir -p "$out"

section() {
  local file="$1"
  local start="$2"
  local stop="$3"
  awk -v start="$start" -v stop="$stop" '
    $0 ~ start { printing = 1 }
    printing && stop != "" && $0 ~ stop && $0 !~ start { exit }
    printing { print }
  ' "$file"
}

rewrite_title() {
  local title="$1"
  awk -v title="$title" 'NR == 1 && /^# / { print "# " title; next } { print }'
}

{
  echo "# Arivu Wiki"
  echo
  section "$root/README.md" "^Self-hosted" "^## What Arivu Does"
  section "$root/README.md" "^## What Arivu Does" "^## How It Runs"
  echo "## Start Here"
  echo
  echo "- [Getting Started](Getting-Started): install, run locally, and configure production."
  echo "- [Using Arivu](Using-Arivu): capture, triage, focus, review, notes, reminders, search, and assistant workflow."
  echo "- [Import Export and Migration](Import-Export-and-Migration): supported imports, exports, Obsidian output, and legacy migration."
  echo "- [Security](Security): sessions, CSRF, SSRF protection, sanitization, exports, and operational limits."
  echo "- [Developer Architecture](Developer-Architecture): codebase layout, runtime, frontend, and testing notes."
  echo
  echo "## Self-Hosting Shape"
  echo
  echo "Arivu runs as a Go application with embedded frontend assets and SQLite persistence. The same binary serves the browser UI, API, workers, CLI commands, and migration tooling."
  echo
  echo "Repository: https://github.com/glnarayanan/arivu"
} > "$out/Home.md"

{
  echo "# Getting Started"
  echo
  section "$root/README.md" "^## Quick Start" "^## Forks"
  echo
  section "$root/openwiki/operations/deployment.md" "^## Environment" "^## Production Notes"
  echo
  section "$root/openwiki/operations/deployment.md" "^## Production Notes" "^## Container"
  echo
  section "$root/README.md" "^## Docker" "^## Migration"
} > "$out/Getting-Started.md"

rewrite_title "Using Arivu" < "$root/openwiki/user-guide.md" > "$out/Using-Arivu.md"

{
  echo "# Import, Export, And Migration"
  echo
  section "$root/openwiki/user-guide.md" "^## Import, Export, And Migration" "^## Useful Admin Notes"
  echo
  sed '1d' "$root/openwiki/domain/migration-guide.md"
} > "$out/Import-Export-and-Migration.md"

{
  echo "# Security"
  echo
  sed '1d' "$root/openwiki/workflows/security-model.md"
  echo
  section "$root/openwiki/workflows/auth-security.md" "^## SSRF Prevention" "^## Backend-Owned HTML Sanitization"
} > "$out/Security.md"

{
  echo "# Developer Architecture"
  echo
  section "$root/openwiki/quickstart.md" "^## High-Level Codebase Layout" ""
  echo
  section "$root/openwiki/architecture/runtime.md" "^## HTTP Runtime" "^## SQLite Database Model"
  echo
  section "$root/openwiki/architecture/runtime.md" "^## SQLite Database Model" "^## Durable Background Jobs Engine"
  echo
  section "$root/openwiki/architecture/runtime.md" "^## Durable Background Jobs Engine" ""
  echo
  section "$root/openwiki/architecture/frontend.md" "^## Embedded Web Console" "^## Companion Browser Extension"
  echo
  section "$root/openwiki/testing/tactics.md" "^## Browser Smoke Checks" "^## Checklist"
} > "$out/Developer-Architecture.md"
