#!/bin/sh
# Instalador de push (estilo curl | sh):
#
#   curl -fsSL https://raw.githubusercontent.com/accentiostudios/push/main/install.sh | sudo sh
#
# Variables opcionales:
#   PUSH_VERSION=v1.2.3   instala una versión concreta (default: latest)
#   PUSH_BINDIR=/usr/bin  directorio de instalación (default: /usr/local/bin)
set -eu

REPO="accentiostudios/push"
VERSION="${PUSH_VERSION:-latest}"
BINDIR="${PUSH_BINDIR:-/usr/local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "push: arquitectura no soportada: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin) ;;
  *) echo "push: sistema no soportado: $os" >&2; exit 1 ;;
esac

asset="push_${os}_${arch}"
if [ "$VERSION" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Descargando push (${os}/${arch})..."
curl -fsSL "${base}/${asset}" -o "${tmp}/push"
curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"

echo "Verificando checksum..."
expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
if [ -z "$expected" ]; then
  echo "push: no se encontró el checksum de ${asset}" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp}/push" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "${tmp}/push" | awk '{print $1}')"
fi
if [ "$expected" != "$actual" ]; then
  echo "push: el checksum no coincide (esperado $expected, obtenido $actual)" >&2
  exit 1
fi

chmod +x "${tmp}/push"
echo "Instalando en ${BINDIR}/push..."
if [ -w "$BINDIR" ]; then
  mv "${tmp}/push" "${BINDIR}/push"
elif command -v sudo >/dev/null 2>&1; then
  sudo mv "${tmp}/push" "${BINDIR}/push"
else
  echo "push: sin permiso de escritura en ${BINDIR} (ejecuta como root o define PUSH_BINDIR)" >&2
  exit 1
fi

echo ""
echo "✓ $("${BINDIR}/push" version 2>/dev/null || echo 'push instalado')"
echo "  Siguiente paso:  sudo push init server"
