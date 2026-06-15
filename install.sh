#!/bin/sh
# Instalador / actualizador de statio (estilo curl | sh):
#
#   curl -fsSL https://raw.githubusercontent.com/accentiostudios/statio/main/install.sh | sudo sh
#
# Re-ejecutarlo ACTUALIZA statio solo si hay una versión más nueva: si ya tienes
# la última, no descarga nada. Variables opcionales:
#   STATIO_VERSION=v1.2.3   instala/actualiza a una versión concreta (default: latest)
#   STATIO_BINDIR=/usr/bin  directorio de instalación (default: /usr/local/bin)
#   STATIO_FORCE=1          (re)instala aunque ya tengas la versión objetivo
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
  *) echo "statio: arquitectura no soportada: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin) ;;
  *) echo "statio: sistema no soportado: $os" >&2; exit 1 ;;
esac

# Resolver el tag objetivo. Para "latest" seguimos el redirect de GitHub
# (.../releases/latest -> .../releases/tag/<tag>) para conocer la versión real.
if [ "$VERSION" = "latest" ]; then
  target="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest" 2>/dev/null | sed -n 's#.*/releases/tag/##p')"
  [ -n "$target" ] || target="latest"   # fallback si no se pudo resolver
else
  target="$VERSION"
fi

# Versión ya instalada (si la hay): `statio version` imprime "statio vX.Y.Z".
installed=""
if command -v statio >/dev/null 2>&1; then
  installed="$(statio version 2>/dev/null | awk '{print $2}')"
fi

# ¿Hace falta actualizar? Sólo nos ponemos "inteligentes" cuando conocemos el
# tag objetivo concreto (no "latest") y no se forzó la reinstalación.
if [ -n "$installed" ] && [ "$target" != "latest" ] && [ -z "$FORCE" ]; then
  if [ "$installed" = "$target" ]; then
    echo "statio ya está en la última versión ($installed). Nada que hacer."
    echo "  (STATIO_FORCE=1 para reinstalar)"
    exit 0
  fi
  # ¿installed >= target? (orden de versiones con sort -V; si no está, instala igual)
  newest="$(printf '%s\n%s\n' "$installed" "$target" | sort -V 2>/dev/null | tail -n1)"
  if [ "$newest" = "$installed" ]; then
    echo "statio instalado ($installed) ya es >= objetivo ($target). Nada que hacer."
    echo "  (STATIO_FORCE=1 para forzar)"
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
  echo "Actualizando statio ${installed} → ${target} (${os}/${arch})..."
else
  echo "Instalando statio ${target} (${os}/${arch})..."
fi
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
if [ -n "$installed" ]; then
  echo "  Si el agente corre como servicio:  sudo systemctl restart statio-agent"
else
  echo "  Siguiente paso:  sudo statio init server"
fi
