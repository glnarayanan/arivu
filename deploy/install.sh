#!/usr/bin/env sh
set -eu

repo="${ARIVU_REPO:-https://github.com/glnarayanan/arivu}"
version="${ARIVU_VERSION:-latest}"
install_dir="${ARIVU_INSTALLER_DIR:-/usr/local/bin}"
attest_repo="${ARIVU_ATTEST_REPO:-glnarayanan/arivu}"

if [ "$(id -u)" -ne 0 ]; then
  echo "Run this installer with sudo or as root." >&2
  exit 1
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
  linux) ;;
  *) echo "Arivu installer supports Linux hosts only." >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac

base="$repo/releases/latest/download"
if [ "$version" != "latest" ]; then
  base="$repo/releases/download/$version"
fi

case "$base" in
  https://*) ;;
  *) echo "Arivu release downloads must use HTTPS." >&2; exit 1 ;;
esac

asset="arivu-installer-linux-$arch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && grep "  $asset\$" SHA256SUMS | sha256sum -c -)
elif command -v shasum >/dev/null 2>&1; then
  expected="$(grep "  $asset\$" "$tmp/SHA256SUMS" | awk '{print $1}')"
  actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
  [ "$expected" = "$actual" ] || { echo "Checksum mismatch for $asset" >&2; exit 1; }
else
  echo "sha256sum or shasum is required to verify installer artifacts." >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "gh is required to verify Arivu release provenance." >&2
  exit 1
fi
gh attestation verify "$tmp/$asset" -R "$attest_repo" >/dev/null

install -m 0755 "$tmp/$asset" "$install_dir/arivu-installer"
if [ "$version" != "latest" ]; then
  exec "$install_dir/arivu-installer" install --version "$version" "$@"
fi
exec "$install_dir/arivu-installer" install "$@"
