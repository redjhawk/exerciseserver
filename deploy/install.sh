#!/bin/sh
# One-time installation on the server (run as root):
#   curl -fsSLO https://raw.githubusercontent.com/redjhawk/exerciseserver/master/deploy/install.sh
#   sudo sh install.sh
# It is safe to run it again to update the unit files or update.sh.
set -eu

REPO="${EXERCISESERVER_REPO:-redjhawk/exerciseserver}"
BRANCH="${EXERCISESERVER_BRANCH:-master}"
UNITS="exerciseserver.service exerciseserver-update.service exerciseserver-update.timer exerciseserver-restart.path exerciseserver-restart.service"

[ "$(id -u)" -eq 0 ] || { echo "ejecuta este script como root (sudo)" >&2; exit 1; }
command -v curl >/dev/null || { echo "falta curl" >&2; exit 1; }
command -v sha256sum >/dev/null || { echo "falta sha256sum" >&2; exit 1; }

SRC=$(cd "$(dirname "$0")" && pwd)
fetch() { # fetch <name> <dest>: use the file next to this script, or download it from GitHub
  if [ -f "$SRC/$1" ]; then
    cp "$SRC/$1" "$2"
  else
    curl -fsSL "https://raw.githubusercontent.com/$REPO/$BRANCH/deploy/$1" -o "$2"
  fi
}

for u in exerciseserver exerciseserver-update; do
  id "$u" >/dev/null 2>&1 || useradd --system --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin "$u"
done

# Binary directory: written only by the updater, readable by the service.
install -d -m 0755 -o root -g root /opt/exerciseserver
install -d -m 0755 -o exerciseserver-update -g exerciseserver-update /opt/exerciseserver/bin

# update.sh belongs to root so the updater cannot change its own code.
install -d -m 0755 /usr/local/lib/exerciseserver
fetch update.sh /usr/local/lib/exerciseserver/update.sh
chown root:root /usr/local/lib/exerciseserver/update.sh
chmod 0755 /usr/local/lib/exerciseserver/update.sh

for u in $UNITS; do
  fetch "$u" "/etc/systemd/system/$u"
  chmod 0644 "/etc/systemd/system/$u"
done

if [ "$REPO" != "redjhawk/exerciseserver" ] && [ ! -f /etc/exerciseserver.env ]; then
  echo "EXERCISESERVER_REPO=$REPO" > /etc/exerciseserver.env
fi

systemctl daemon-reload
systemctl enable exerciseserver.service
systemctl enable --now exerciseserver-restart.path exerciseserver-update.timer

echo "Descargando la última release…"
systemctl start exerciseserver-update.service || {
  echo "La descarga falló. Mira: journalctl -u exerciseserver-update" >&2
  exit 1
}
sleep 2
systemctl --no-pager --lines=0 status exerciseserver.service || true

cat <<EOF

Instalado. Para crear el usuario administrador (o resetear su contraseña):

  sudo -u exerciseserver EXERCISESERVER_DATA_DIR=/var/lib/exerciseserver /opt/exerciseserver/bin/exerciseserver admin-password

Escucha en 127.0.0.1:8090 (cámbialo con EXERCISESERVER_ADDR en /etc/exerciseserver.env).
Logs: journalctl -u exerciseserver -f   ·   Actualizaciones: journalctl -u exerciseserver-update
EOF
