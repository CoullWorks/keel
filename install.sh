#!/bin/sh
# keel installer — downloads the latest release binary for your OS/arch.
# Usage:  curl -fsSL https://raw.githubusercontent.com/coullworks/keel/main/install.sh | sh
# Env:    KEEL_INSTALL_DIR (default ~/.local/bin), KEEL_VERSION (default latest)
set -eu

REPO="coullworks/keel"
INSTALL_DIR="${KEEL_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${KEEL_VERSION:-latest}"

say()  { printf '  %s\n' "$*"; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux)  os=linux ;;
  darwin) os=darwin ;;
  *) die "unsupported OS: $os (build from source: go install github.com/$REPO/cmd/keel@latest)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported arch: $arch" ;;
esac

asset="keel_${os}_${arch}"
if [ "$VERSION" = "latest" ]; then
  url="https://github.com/$REPO/releases/latest/download/$asset"
else
  url="https://github.com/$REPO/releases/download/$VERSION/$asset"
fi

command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || die "need curl or wget"

say "keel installer"
say "target: $os/$arch"
say "from:   $url"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$url" -o "$tmp" || die "download failed (no release asset for $asset yet? try: go install github.com/$REPO/cmd/keel@latest)"
else
  wget -qO "$tmp" "$url" || die "download failed"
fi

# Verify the published SHA-256 before this becomes an executable on your PATH.
#
# `keel selfupdate` already refuses to install an asset with no published
# checksum, and this script installs the same asset from the same place - so
# without this, the FIRST keel you ever run was the one binary nobody checked,
# and every update after it was strictly verified. That is the wrong way round:
# the first install is what bootstraps the rest.
#
# Same failure stance as selfupdate: a missing, malformed or mismatched checksum
# aborts. Skipping the check on a 404 is how a substituted asset gets installed
# in silence.
#
# This proves integrity, not authenticity. The binary and its checksum come from
# the same host, so it catches truncation, corruption and a swapped asset, but a
# compromised release host could publish a matching pair. Signing is the next
# step, and it belongs here and in selfupdate together.
sum_tmp="$(mktemp)"
trap 'rm -f "$tmp" "$sum_tmp"' EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$url.sha256" -o "$sum_tmp" || die "no published checksum at $url.sha256, refusing to install"
else
  wget -qO "$sum_tmp" "$url.sha256" || die "no published checksum at $url.sha256, refusing to install"
fi

# sha256sum on Linux, shasum on macOS. Both print "<hex>  <file>".
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp" | cut -d' ' -f1)"
elif command -v shasum >/dev/null 2>&1; then
  got="$(shasum -a 256 "$tmp" | cut -d' ' -f1)"
else
  die "need sha256sum or shasum to verify the download"
fi

# First field, so a "<hex>  keel_linux_amd64" line works as well as a bare hash.
want="$(cut -d' ' -f1 <"$sum_tmp" | tr -d '\r\n' | tr 'A-F' 'a-f')"
got="$(printf '%s' "$got" | tr 'A-F' 'a-f')"
[ ${#want} -eq 64 ] || die "checksum at $url.sha256 is malformed, refusing to install"
[ "$want" = "$got" ] || die "checksum mismatch for $asset, refusing to install (expected $want, got $got)"
say "checksum: ok"

mkdir -p "$INSTALL_DIR"
chmod +x "$tmp"
mv "$tmp" "$INSTALL_DIR/keel"
trap - EXIT
rm -f "$sum_tmp"

say "installed: $INSTALL_DIR/keel"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) : ;;
  *) say "add to PATH:  export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
"$INSTALL_DIR/keel" --version 2>/dev/null || true
say "done — run 'keel' to get started."
