#!/bin/sh
# Downloads the latest GitHub release of exerciseserver if it is newer than the installed one.
# Runs as the unprivileged user exerciseserver-update (see exerciseserver-update.service).
# After installing a new binary it touches .restart; exerciseserver-restart.path (root) restarts the service.
set -eu

REPO="${EXERCISESERVER_REPO:-redjhawk/exerciseserver}"
ASSET="exerciseserver-linux-amd64"
DIR="${EXERCISESERVER_BIN_DIR:-/opt/exerciseserver/bin}"

# /releases/latest redirects to /releases/tag/<tag>; no API token or jq needed.
url=$(curl -fsS --max-time 30 -o /dev/null -w '%{redirect_url}' "https://github.com/$REPO/releases/latest")
case "$url" in
  */releases/tag/*) tag=${url##*/releases/tag/} ;;
  *) echo "no se encontró ninguna release en $REPO" >&2; exit 1 ;;
esac
case "$tag" in
  v[0-9A-Za-z]*) ;;
  *) echo "tag inesperado: $tag" >&2; exit 1 ;;
esac
case "$tag" in
  *[!0-9A-Za-z._-]*) echo "tag inesperado: $tag" >&2; exit 1 ;;
esac

current=$(cat "$DIR/VERSION" 2>/dev/null || true)
if [ "$tag" = "$current" ]; then
  exit 0
fi
echo "actualizando ${current:-(nada)} -> $tag"

tmp=$(mktemp -d "$DIR/.download.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

base="https://github.com/$REPO/releases/download/$tag"
curl -fsSL --retry 3 --max-time 300 -o "$tmp/$ASSET" "$base/$ASSET"
curl -fsSL --retry 3 --max-time 30 -o "$tmp/$ASSET.sha256" "$base/$ASSET.sha256"
(cd "$tmp" && sha256sum -c --status "$ASSET.sha256") || { echo "checksum incorrecto" >&2; exit 1; }

chmod 0755 "$tmp/$ASSET"
got=$("$tmp/$ASSET" version)
if [ "$got" != "$tag" ]; then
  echo "el binario descargado dice ser $got, se esperaba $tag" >&2
  exit 1
fi

mv -f "$tmp/$ASSET" "$DIR/exerciseserver"
echo "$tag" > "$DIR/VERSION"
date -u +%Y-%m-%dT%H:%M:%SZ > "$DIR/.restart"
echo "instalado $tag"
