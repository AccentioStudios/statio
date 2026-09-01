#!/bin/sh
# statio installer and updater, in the curl | sh style:
#
#   curl -fsSL https://raw.githubusercontent.com/accentiostudios/statio/main/install.sh | sudo sh
#
# Re-running it updates statio only when a newer version exists: with the latest
# already installed, it downloads nothing. Optional variables:
#   STATIO_VERSION=v1.2.3   install or update to an exact version (default: latest)
#   STATIO_BINDIR=/usr/bin  install directory (default: /usr/local/bin)
#   STATIO_FORCE=1          install even when the target version is already there
set -eu

REPO="accentiostudios/statio"
VERSION="${STATIO_VERSION:-latest}"
BINDIR="${STATIO_BINDIR:-/usr/local/bin}"
FORCE="${STATIO_FORCE:-}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "statio: unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin) ;;
  *) echo "statio: unsupported system: $os" >&2; exit 1 ;;
esac

# Resolve the target tag. For "latest" we follow GitHub's redirect
# (.../releases/latest -> .../releases/tag/<tag>) to learn the real version.
if [ "$VERSION" = "latest" ]; then
  target="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest" 2>/dev/null | sed -n 's#.*/releases/tag/##p')"
  [ -n "$target" ] || target="latest"   # fallback when the redirect gives us nothing
else
  target="$VERSION"
fi

# The version already installed, if any: `statio version` prints "statio vX.Y.Z".
installed=""
if command -v statio >/dev/null 2>&1; then
  installed="$(statio version 2>/dev/null | awk '{print $2}')"
fi

# Does anything need updating? We only compare when we know the exact target tag
# (not "latest") and the caller did not force a reinstall.
if [ -n "$installed" ] && [ "$target" != "latest" ] && [ -z "$FORCE" ]; then
  if [ "$installed" = "$target" ]; then
    echo "statio is already at the latest version ($installed). Nothing to do."
    echo "  (STATIO_FORCE=1 reinstalls it)"
    exit 0
  fi
  # Is installed >= target? Version order via sort -V; without it, install anyway.
  newest="$(printf '%s\n%s\n' "$installed" "$target" | sort -V 2>/dev/null | tail -n1)"
  if [ "$newest" = "$installed" ]; then
    echo "The installed statio ($installed) is already >= the target ($target). Nothing to do."
    echo "  (STATIO_FORCE=1 forces it)"
    exit 0
  fi
fi

asset="statio_${os}_${arch}"
if [ "$target" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  base="https://github.com/${REPO}/releases/download/${target}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ -n "$installed" ]; then
  echo "Updating statio ${installed} to ${target} (${os}/${arch})..."
else
  echo "Installing statio ${target} (${os}/${arch})..."
fi
curl -fsSL "${base}/${asset}" -o "${tmp}/statio"
curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"

echo "Verifying the checksum..."
expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
if [ -z "$expected" ]; then
  echo "statio: no checksum found for ${asset}" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp}/statio" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "${tmp}/statio" | awk '{print $1}')"
fi
if [ "$expected" != "$actual" ]; then
  echo "statio: the checksum does not match (expected $expected, got $actual)" >&2
  exit 1
fi

chmod +x "${tmp}/statio"
echo "Installing to ${BINDIR}/statio..."
if [ -w "$BINDIR" ]; then
  mv "${tmp}/statio" "${BINDIR}/statio"
elif command -v sudo >/dev/null 2>&1; then
  sudo mv "${tmp}/statio" "${BINDIR}/statio"
else
  echo "statio: no write permission on ${BINDIR}. Run as root, or set STATIO_BINDIR." >&2
  exit 1
fi

echo ""
echo "✓ $("${BINDIR}/statio" version 2>/dev/null || echo 'statio installed')"
if [ -n "$installed" ]; then
  # On an update, reload the agent so it picks up the new binary. We only touch
  # the unit when it is active.
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet statio-agent 2>/dev/null; then
    echo "  Restarting the agent (statio-agent) on the new binary..."
    systemctl restart statio-agent && echo "  ✓ agent restarted"
  else
    echo "  (no active statio-agent, so nothing to restart)"
  fi
else
  echo "  Next step:  sudo statio init server"
fi
