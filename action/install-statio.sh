#!/usr/bin/env bash
# Downloads a version-pinned `statio` binary for the runner, verifies its checksum against
# the release checksums.txt, and puts it on PATH. A pinned action ref (statio-action@v1)
# implies a pinned binary, so the wire schema and agent stay in lockstep.
set -euo pipefail

VERSION="${1:-v1}"
REPO="${STATIO_RELEASE_REPO:-accentiostudios/statio}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

# A bare major (v1) or "latest" maps to the newest published release: the moving v1
# action tag pins the action code; the binary tracks the latest matching release. An exact
# vX.Y.Z is used verbatim. Resolve the tag by following the /releases/latest redirect.
if [ "$VERSION" = "latest" ] || echo "$VERSION" | grep -Eq '^v[0-9]+$'; then
  resolved="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest" | sed -n 's#.*/releases/tag/##p')"
  if [ -n "$resolved" ]; then
    echo "Resolved ${VERSION} -> ${resolved}"
    VERSION="$resolved"
  fi
fi

asset="statio_${os}_${arch}"
base="https://github.com/${REPO}/releases/download/${VERSION}"
bindir="${RUNNER_TEMP:-/tmp}/statio-bin"
mkdir -p "$bindir"

echo "Downloading ${asset} (${VERSION})..."
curl -fsSL "${base}/${asset}" -o "${bindir}/statio"
curl -fsSL "${base}/checksums.txt" -o "${bindir}/checksums.txt"

echo "Verifying checksum..."
expected="$(grep " ${asset}\$" "${bindir}/checksums.txt" | awk '{print $1}')"
actual="$(sha256sum "${bindir}/statio" | awk '{print $1}')"
if [[ -z "$expected" || "$expected" != "$actual" ]]; then
  echo "checksum verification failed for ${asset}" >&2
  exit 1
fi

chmod +x "${bindir}/statio"
echo "$bindir" >> "$GITHUB_PATH"
echo "statio ${VERSION} installed."
