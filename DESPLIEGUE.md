# Despliegue de exerciseserver en un servidor Linux

Esta guía instala exerciseserver en un servidor Linux con systemd (por ejemplo, Debian) y lo publica a través de Cloudflare Tunnel. Funciona así:

- El servidor no recibe nada del sistema de integración continua. Cada 5 minutos comprueba en GitHub si hay una release nueva, la descarga y verifica su suma SHA-256.
- Lo que se conecta a Internet se ejecuta con un usuario sin privilegios, `actualizador`.
- Lo único que hace systemd como root es reiniciar el servicio cuando cambia la versión. Ese paso no descarga ni procesa nada.
- La aplicación se ejecuta con su propio usuario, `exerciseserver`, y solo puede escribir en `/var/lib/exerciseserver`.

| Pieza | Valor |
|---|---|
| Repositorio | `redjhawk/exerciseserver` |
| Fichero de la release | `exerciseserver-linux-amd64`, más su `.sha256` |
| Binario instalado | `/opt/exerciseserver/exerciseserver-linux-amd64` |
| Versión instalada | `/opt/exerciseserver/version` |
| Datos (SQLite y ficheros subidos) | `/var/lib/exerciseserver` |
| Escucha | `127.0.0.1:8090` |
| Unidades systemd | `actualizar-exerciseserver.{service,timer}`, `exerciseserver-reinicio.{path,service}`, `exerciseserver.service` |

> **Nota:** el repositorio incluye también [`deploy/install.sh`](deploy/install.sh), que monta algo equivalente de forma automática y con otros nombres. Usa uno de los dos métodos, no los dos a la vez.

## Requisitos previos

- Un servidor Linux x86_64 con systemd y con `curl` y `jq` instalados (`sudo apt install curl jq`).
- Salida HTTPS hacia GitHub. Si el servidor tiene la salida restringida, hay que permitir `api.github.com`, `github.com`, `release-assets.githubusercontent.com` y `objects.githubusercontent.com`. GitHub redirige la descarga de los ficheros de las releases a estos dos últimos.
- `cloudflared` instalado como servicio, con un túnel ya creado.

exerciseserver no se conecta a ningún servicio externo. Solo recibe peticiones locales de `cloudflared`.

Cada push a `master` publica una release nueva con el workflow [`release.yml`](.github/workflows/release.yml).

## 1. Usuarios y carpetas

```bash
sudo adduser --system --group --no-create-home actualizador
sudo adduser --system --group --no-create-home exerciseserver
sudo mkdir -p /opt/exerciseserver /var/lib/exerciseserver
sudo chown actualizador:actualizador /opt/exerciseserver
sudo chown exerciseserver:exerciseserver /var/lib/exerciseserver
sudo chmod 750 /var/lib/exerciseserver
```

- `/opt/exerciseserver` pertenece a `actualizador`: solo él puede cambiar el binario. La aplicación puede leerlo y ejecutarlo, pero no modificarlo.
- `/var/lib/exerciseserver` pertenece a `exerciseserver`: es lo único en lo que la aplicación puede escribir.

## 2. Script de actualización

Crear `/usr/local/bin/actualizar-exerciseserver`, propiedad de root y con `chmod 755`. No reinicia nada: solo descarga, verifica e instala.

```sh
#!/bin/sh
set -eu
REPO=redjhawk/exerciseserver
DIR=/opt/exerciseserver
BIN=exerciseserver-linux-amd64
# Límites de tiempo: si un destino está bloqueado, falla en segundos en lugar de quedarse colgado
CURL="curl -fsSL --connect-timeout 15 --max-time 300"

TAG=$($CURL "https://api.github.com/repos/$REPO/releases/latest" | jq -r .tag_name)
case "$TAG" in
  v[0-9]*) ;;
  *) echo "Etiqueta inesperada: $TAG" >&2; exit 1 ;;
esac
[ "$TAG" = "$(cat $DIR/version 2>/dev/null || true)" ] && exit 0

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
$CURL -o "$TMP/$BIN" "https://github.com/$REPO/releases/download/$TAG/$BIN"
$CURL -o "$TMP/$BIN.sha256" "https://github.com/$REPO/releases/download/$TAG/$BIN.sha256"

# Verificación de la suma SHA-256
(cd "$TMP" && sha256sum -c --status "$BIN.sha256") || { echo "Suma SHA-256 incorrecta" >&2; exit 1; }

# El binario dice su versión: debe coincidir con la etiqueta
chmod 755 "$TMP/$BIN"
[ "$("$TMP/$BIN" version)" = "$TAG" ] || { echo "El binario no corresponde a $TAG" >&2; exit 1; }

[ -f "$DIR/$BIN" ] && cp "$DIR/$BIN" "$DIR/$BIN.anterior"
install -m 755 "$TMP/$BIN" "$DIR/$BIN"
echo "$TAG" > $DIR/version
echo "Instalada $TAG"
```

```bash
sudo nano /usr/local/bin/actualizar-exerciseserver
sudo chown root:root /usr/local/bin/actualizar-exerciseserver
sudo chmod 755 /usr/local/bin/actualizar-exerciseserver
```

## 3. Servicio y temporizador del actualizador

`/etc/systemd/system/actualizar-exerciseserver.service`:

```ini
[Unit]
Description=Descarga la última release de exerciseserver
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=actualizador
Group=actualizador
ExecStart=/usr/local/bin/actualizar-exerciseserver
ProtectSystem=strict
ReadWritePaths=/opt/exerciseserver
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
CapabilityBoundingSet=
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
```

`/etc/systemd/system/actualizar-exerciseserver.timer`:

```ini
[Unit]
Description=Comprueba nuevas releases de exerciseserver cada 5 minutos

[Timer]
# Cada 5 minutos de reloj; Persistent recupera la ejecución perdida si el servidor estaba apagado
OnCalendar=*:0/5
Persistent=true

[Install]
WantedBy=timers.target
```

## 4. Reinicio automático cuando cambia la versión

`/etc/systemd/system/exerciseserver-reinicio.path`:

```ini
[Unit]
Description=Vigila nuevas versiones de exerciseserver

[Path]
PathChanged=/opt/exerciseserver/version

[Install]
WantedBy=multi-user.target
```

`/etc/systemd/system/exerciseserver-reinicio.service` es lo único que se ejecuta como root, y no descarga ni procesa nada:

```ini
[Unit]
Description=Reinicia exerciseserver tras una actualización

[Service]
Type=oneshot
ExecStart=/usr/bin/systemctl restart exerciseserver.service
```

## 5. Servicio de la aplicación

`/etc/systemd/system/exerciseserver.service`. La aplicación lee su dirección de `EXERCISESERVER_ADDR` y guarda la base de datos y los ficheros subidos en `EXERCISESERVER_DATA_DIR`.

```ini
[Unit]
Description=exerciseserver (ejercicios)
After=network-online.target
Wants=network-online.target
# Hasta que llegue el primer binario no hay nada que arrancar
ConditionPathExists=/opt/exerciseserver/exerciseserver-linux-amd64

[Service]
Type=simple
User=exerciseserver
Group=exerciseserver
Environment=EXERCISESERVER_DATA_DIR=/var/lib/exerciseserver
Environment=EXERCISESERVER_ADDR=127.0.0.1:8090
ExecStart=/opt/exerciseserver/exerciseserver-linux-amd64 serve
Restart=on-failure
RestartSec=5
ProtectSystem=strict
ReadWritePaths=/var/lib/exerciseserver
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
NoNewPrivileges=yes
CapabilityBoundingSet=
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
MemoryDenyWriteExecute=yes

[Install]
WantedBy=multi-user.target
```

Escucha solo en `127.0.0.1`, así que no es accesible desde la red, únicamente a través del túnel. Si ese puerto ya está ocupado, cambia `EXERCISESERVER_ADDR` y usa el mismo puerto en el paso 8.

## 6. Activar

```bash
sudo systemctl daemon-reload
sudo systemctl enable exerciseserver.service
sudo systemctl enable --now actualizar-exerciseserver.timer exerciseserver-reinicio.path
# Primera descarga sin esperar al temporizador
sudo systemctl start actualizar-exerciseserver.service
```

Comprobar:

```bash
journalctl -u actualizar-exerciseserver -n 20 --no-pager   # "Instalada v..."
cat /opt/exerciseserver/version
systemctl status exerciseserver --no-pager                 # active (running)
curl -s http://127.0.0.1:8090/healthz                      # ok
systemctl list-timers actualizar-exerciseserver.timer      # NEXT con hora
```

## 7. Usuario administrador

```bash
sudo -u exerciseserver EXERCISESERVER_DATA_DIR=/var/lib/exerciseserver \
  /opt/exerciseserver/exerciseserver-linux-amd64 admin-password
```

El comando muestra `Username: admin` y una contraseña aleatoria. Guárdala. Volver a ejecutarlo crea una contraseña nueva y cierra las sesiones abiertas del administrador. Los demás usuarios se crean después desde la web, en **Usuarios**.

## 8. Cloudflare Tunnel

En el panel de Cloudflare Zero Trust, dentro del túnel, añadir un **nombre público**:

| Campo | Valor |
|---|---|
| Subdominio | por ejemplo `ejercicios.tu-dominio.example` |
| Servicio | `http://127.0.0.1:8090` |

Cloudflare crea el registro DNS y sirve el certificado.

Detrás de Cloudflare, la aplicación hace lo siguiente:

- Marca la cookie de sesión como `Secure`, porque `cloudflared` envía `X-Forwarded-Proto: https`.
- Toma la IP real del visitante de `CF-Connecting-IP` para limitar los intentos de login (10 fallos cada 15 minutos por IP).

## 9. Carga inicial de los ejercicios

Hay dos formas de hacerlo:

- **Desde la web:** en **Ficheros**, arrastrar los `.html` y `.pdf` y elegir el nivel y las etiquetas.
- **Importación masiva desde la línea de comandos:** las subcarpetas se convierten en etiquetas. Copiar la carpeta al servidor:

  ```bash
  scp -r ./ejercicios usuario@servidor:/tmp/ejercicios
  ```

  En el servidor:

  ```bash
  sudo chown -R exerciseserver:exerciseserver /tmp/ejercicios
  sudo -u exerciseserver EXERCISESERVER_DATA_DIR=/var/lib/exerciseserver \
    /opt/exerciseserver/exerciseserver-linux-amd64 import -level "1º Primaria" /tmp/ejercicios
  sudo rm -rf /tmp/ejercicios
  ```

  `-level` y `-tags a,b` se aplican a todos los ficheros. Los ficheros que ya existen (mismo nombre y tamaño) se omiten. Se puede ejecutar con el servicio en marcha.

## 10. Vuelta atrás y copias de seguridad

Volver a la versión anterior:

```bash
sudo cp /opt/exerciseserver/exerciseserver-linux-amd64.anterior /opt/exerciseserver/exerciseserver-linux-amd64
sudo systemctl restart exerciseserver.service
```

La siguiente release que se publique volverá a instalarse sola. Para quedarse en la versión anterior hasta corregir el problema, parar el temporizador con `sudo systemctl stop actualizar-exerciseserver.timer`.

Para la copia de seguridad, todo lo que cambia está en `/var/lib/exerciseserver`: `exerciseserver.sqlite*` y `files/`. Así se hace una copia coherente con el servicio parado:

```bash
sudo systemctl stop exerciseserver
sudo tar czf /root/exerciseserver-$(date +%F).tgz -C /var/lib exerciseserver
sudo systemctl start exerciseserver
```

## 11. Vigilancia (opcional)

La ruta `/healthz` responde `ok` sin necesidad de iniciar sesión. Sirve para cualquier vigilante externo (UptimeRobot, Healthchecks.io o un script propio). Cuando el servicio está caído, Cloudflare devuelve 502 o 504:

```sh
CODIGO=$(curl -s -o /dev/null -w '%{http_code}' -m 10 https://ejercicios.tu-dominio.example/healthz)
[ "$CODIGO" = "200" ] || echo "exerciseserver no responde (código $CODIGO)"
```

## Problemas y soluciones

- **Error 404 en el actualizador:** el nombre del binario no coincide con el de la release (`exerciseserver-linux-amd64`), o todavía no hay ninguna release publicada.
- **"Suma SHA-256 incorrecta":** la descarga quedó incompleta (por ejemplo, porque está bloqueado uno de los dominios de GitHub de los requisitos previos) o el fichero fue alterado. No se instala nada.
- **La descarga se queda colgada:** la salida hacia `release-assets.githubusercontent.com` u `objects.githubusercontent.com` está bloqueada.
- **`exerciseserver.service` no arranca (condición no cumplida):** todavía no ha llegado el primer binario. Lanza `sudo systemctl start actualizar-exerciseserver.service`.
- **`status=1` al arrancar:** normalmente `/var/lib/exerciseserver` no pertenece a `exerciseserver`. Revisa el paso 1. Los detalles salen en `journalctl -u exerciseserver -n 50`.
- **"Demasiados intentos" al entrar:** son 10 fallos en 15 minutos desde la misma IP. Espera, o reinicia el servicio para vaciar el contador.
