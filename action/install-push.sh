#!/usr/bin/env bash
# Downloads a version-pinned `push` binary for the runner, verifies its checksum against
# the release checksums.txt, and puts it on PATH. A pinned action ref (push-action@v1)
# implies a pinned binary, so the wire schema and agent stay in lockstep.
set -euo pipefail

VERSION="${1:-v1}"
REPO="${PUSH_RELEASE_REPO:-accentiostudios/push}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

asset="push_${os}_${arch}"
base="https://github.com/${REPO}/releases/download/${VERSION}"
bindir="${RUNNER_TEMP:-/tmp}/push-bin"
mkdir -p "$bindir"

echo "Downloading ${asset} (${VERSION})..."
curl -fsSL "${base}/${asset}" -o "${bindir}/push"
curl -fsSL "${base}/checksums.txt" -o "${bindir}/checksums.txt"

echo "Verifying checksum..."
expected="$(grep " ${asset}\$" "${bindir}/checksums.txt" | awk '{print $1}')"
actual="$(sha256sum "${bindir}/push" | awk '{print $1}')"
if [[ -z "$expected" || "$expected" != "$actual" ]]; then
  echo "checksum verification failed for ${asset}" >&2
  exit 1
fi

chmod +x "${bindir}/push"
echo "$bindir" >> "$GITHUB_PATH"
echo "push ${VERSION} installed."
