#!/bin/sh
# Instalador de statio (estilo curl | sh):
#
#   curl -fsSL https://raw.githubusercontent.com/accentiostudios/statio/main/install.sh | sudo sh
#
# Variables opcionales:
#   STATIO_VERSION=v1.2.3   instala una versión concreta (default: latest)
#   STATIO_BINDIR=/usr/bin  directorio de instalación (default: /usr/local/bin)
set -eu

REPO="accentiostudios/statio"
VERSION="${STATIO_VERSION:-latest}"
BINDIR="${STATIO_BINDIR:-/usr/local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "statio: arquitectura no soportada: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin) ;;
  *) echo "statio: sistema no soportado: $os" >&2; exit 1 ;;
esac

asset="statio_${os}_${arch}"
if [ "$VERSION" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Descargando statio (${os}/${arch})..."
curl -fsSL "${base}/${asset}" -o "${tmp}/statio"
curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"

echo "Verificando checksum..."
expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
if [ -z "$expected" ]; then
  echo "statio: no se encontró el checksum de ${asset}" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp}/statio" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "${tmp}/statio" | awk '{print $1}')"
fi
if [ "$expected" != "$actual" ]; then
  echo "statio: el checksum no coincide (esperado $expected, obtenido $actual)" >&2
  exit 1
fi

chmod +x "${tmp}/statio"
echo "Instalando en ${BINDIR}/statio..."
if [ -w "$BINDIR" ]; then
  mv "${tmp}/statio" "${BINDIR}/statio"
elif command -v sudo >/dev/null 2>&1; then
  sudo mv "${tmp}/statio" "${BINDIR}/statio"
else
  echo "statio: sin permiso de escritura en ${BINDIR} (ejecuta como root o define STATIO_BINDIR)" >&2
  exit 1
fi

echo ""
echo "✓ $("${BINDIR}/statio" version 2>/dev/null || echo 'statio instalado')"
echo "  Siguiente paso:  sudo statio init server"
