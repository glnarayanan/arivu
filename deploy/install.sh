#!/usr/bin/env sh
set -eu

repo="${ARIVU_REPO:-https://github.com/glnarayanan/arivu}"
version="${ARIVU_VERSION:-latest}"
install_dir="${ARIVU_INSTALLER_DIR:-/usr/local/bin}"
attest_repo="${ARIVU_ATTEST_REPO:-glnarayanan/arivu}"
gh_keyring_sha256="6084d5d7bd8e288441e0e94fc6275570895da18e6751f70f057485dc2d1a811b"

fail() {
  echo "$1" >&2
  exit 1
}

verify_sha256_file() {
  file="$1"
  expected="$2"
  label="$3"

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  else
    fail "sha256sum or shasum is required to verify $label."
  fi

  [ "$actual" = "$expected" ] || fail "Checksum mismatch for $label"
}

install_gh_with_apt() {
  command -v apt-get >/dev/null 2>&1 || fail "gh is required to verify Arivu release provenance. Install GitHub CLI manually and rerun this bootstrap."
  command -v dpkg >/dev/null 2>&1 || fail "dpkg is required to configure the official GitHub CLI apt repository."
  [ -r /etc/os-release ] || fail "gh is required to verify Arivu release provenance. Install GitHub CLI manually and rerun this bootstrap."
  # shellcheck disable=SC1091
  . /etc/os-release
  case "${ID:-}" in
    debian|ubuntu) ;;
    *) fail "Automatic gh installation is supported on Debian and Ubuntu only. Install GitHub CLI manually and rerun this bootstrap." ;;
  esac

  echo "GitHub CLI not found; installing gh from the official GitHub CLI apt repository."
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl

  mkdir -p -m 755 /etc/apt/keyrings /etc/apt/sources.list.d
  curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o "$tmp/githubcli-archive-keyring.gpg"
  verify_sha256_file "$tmp/githubcli-archive-keyring.gpg" "$gh_keyring_sha256" "GitHub CLI apt keyring"
  install -m 0644 "$tmp/githubcli-archive-keyring.gpg" /etc/apt/keyrings/githubcli-archive-keyring.gpg

  arch_deb="$(dpkg --print-architecture)"
  echo "deb [arch=$arch_deb signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends gh
}

ensure_gh() {
  if command -v gh >/dev/null 2>&1; then
    return 0
  fi

  install_gh_with_apt
  command -v gh >/dev/null 2>&1 || fail "GitHub CLI installation completed, but gh is still unavailable on PATH."
}

if [ "$(id -u)" -ne 0 ]; then
  fail "Run this installer with sudo or as root."
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
  linux) ;;
  *) fail "Arivu installer supports Linux hosts only." ;;
esac
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "Unsupported architecture: $arch" ;;
esac

base="$repo/releases/latest/download"
if [ "$version" != "latest" ]; then
  base="$repo/releases/download/$version"
fi

case "$base" in
  https://*) ;;
  *) fail "Arivu release downloads must use HTTPS." ;;
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
  [ "$expected" = "$actual" ] || fail "Checksum mismatch for $asset"
else
  fail "sha256sum or shasum is required to verify installer artifacts."
fi

ensure_gh
gh attestation verify "$tmp/$asset" -R "$attest_repo" >/dev/null

install -m 0755 "$tmp/$asset" "$install_dir/arivu-installer"
if [ "$version" != "latest" ]; then
  exec "$install_dir/arivu-installer" install --version "$version" "$@"
fi
exec "$install_dir/arivu-installer" install "$@"
