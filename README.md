# exerciseserver

Servidor de ejercicios (HTML y PDF) para niños. Es un único binario en Go con SQLite embebido.

- **Usuarios:** entran con usuario y contraseña y ven el catálogo. Pueden filtrar por nivel, tipo, etiquetas y texto. Los HTML se abren dentro de la página y los PDF se pueden descargar.
- **Administración** (`/admin`): permite subir ficheros (varios a la vez, arrastrando), editar el título, la descripción, el nivel y las etiquetas, y gestionar usuarios, niveles y etiquetas.

## Desarrollo

```bash
go run ./cmd/exerciseserver admin-password   # crea el usuario admin y muestra su contraseña
go run ./cmd/exerciseserver                  # http://127.0.0.1:8090 (datos en ./data)
go test ./...
```

Variables de entorno:

- `EXERCISESERVER_DATA_DIR`: directorio de datos. Por defecto es `./data`.
- `EXERCISESERVER_ADDR`: dirección de escucha. Por defecto es `127.0.0.1:8090`.

Todo lo que cambia está en el directorio de datos: `exerciseserver.sqlite` y `files/`. Para hacer una copia de seguridad, copia ese directorio.

## Importar ejercicios existentes

```bash
exerciseserver import -level "1º Primaria" -tags inglés ./carpeta
```

La importación recorre las carpetas de forma recursiva. Cada nombre de subcarpeta se añade como etiqueta: `matemáticas/sumas/x.html` recibe las etiquetas `matemáticas` y `sumas`. Para desactivarlo, usa `-folder-tags=false`. Los ficheros que ya existen (mismo nombre y tamaño) se omiten. El título se toma del `<title>` del HTML o, si no hay, del nombre del fichero.

En el servidor hay que ejecutarlo como el usuario del servicio, y ese usuario tiene que poder leer los ficheros:

```bash
sudo -u exerciseserver EXERCISESERVER_DATA_DIR=/var/lib/exerciseserver \
  /opt/exerciseserver/bin/exerciseserver import -level "1º Primaria" /tmp/ejercicios
```

## Subir ejercicios desde Claude (servidor MCP)

El servidor incluye un servidor MCP en `/mcp` para que Claude consulte, publique y modifique ejercicios directamente. Ofrece estas herramientas:

| Herramienta | Qué hace |
|---|---|
| `listar_niveles`, `listar_etiquetas` | Devuelven los niveles y las etiquetas que existen, con su número de ejercicios. |
| `listar_ejercicios(texto?, nivel?, etiqueta?, tipo?)` | Lista el catálogo con id, título, descripción, tipo, fichero, tamaño, nivel, etiquetas, fecha y enlace. Los filtros no distinguen mayúsculas ni tildes. |
| `leer_ejercicio(id)` | Devuelve toda la información de un ejercicio y, si es HTML, su código completo. |
| `subir_ejercicio(nombre_fichero, html, titulo?, descripcion?, nivel?, etiquetas?)` | Publica un HTML nuevo y devuelve el enlace `/ver/{id}`. Si ya existe un fichero con el mismo nombre y tamaño, no lo duplica. |
| `actualizar_ejercicio(id, html?, nombre_fichero?, titulo?, descripcion?, nivel?, etiquetas?)` | Modifica un ejercicio conservando su id y su enlace. Solo cambia los campos que se envían: `html` sustituye el contenido (solo en ejercicios HTML), `nivel: ""` lo deja sin nivel y `etiquetas: []` quita todas. Si algún dato no es válido, no se cambia nada. |

El nivel tiene que existir; las etiquetas se crean si hace falta. Para modificar un ejercicio, lo normal es pedírselo a Claude («cambia el ejercicio de sumas para que tenga 10 preguntas»). Claude lo busca con `listar_ejercicios`, lo lee con `leer_ejercicio` y lo guarda con `actualizar_ejercicio`. Actualizar sustituye el HTML anterior y no guarda una copia; si quieres conservarlo, descárgalo antes.

En [`INSTRUCCIONES_CLAUDE.md`](INSTRUCCIONES_CLAUDE.md) están las instrucciones para la IA: cómo tiene que ser el HTML, cómo usar la IA de la plataforma (y cómo funcionar sin ella) y cómo subir o actualizar ejercicios. El fichero va incluido en el binario, así que siempre corresponde a la versión instalada, y se puede obtener de tres formas:

- **Herramienta MCP `obtener_instrucciones`**: la IA conectada lo lee directamente del servidor. Al conectarse, el MCP ya le indica que lo haga antes de crear o modificar un ejercicio.
- **Admin → Tokens → Instrucciones para la IA**: botones «Ver» y «Descargar», y una frase lista para copiar y pegarle a la IA.
- **`/instrucciones-ia.md`**: para cualquier usuario con sesión iniciada (`?descargar=1` lo descarga).

Para usarlo hace falta un **token de API**, que se gestiona en **Admin → Tokens**:

1. Crea el token con un nombre y una duración: 1 hora, 1 día, 7 días, 30 días o sin caducidad. Se muestra **una sola vez**, junto con la URL y el comando listos para copiar.
2. **Revocar** lo desactiva al momento y lo deja en la lista como registro. **Borrar** lo elimina del todo.
3. La lista muestra el estado (activo, caducado o revocado), la fecha de caducidad y el último uso.

En la base de datos solo se guarda el hash del token. El token solo sirve para el MCP (consultar, subir y modificar ejercicios; no puede borrarlos) y no da acceso a la interfaz web ni a la administración.

**Claude Code** (con el token en la cabecera):

```bash
claude mcp add --transport http ejercicios https://ejercicios.tudominio.com/mcp \
  --header "Authorization: Bearer exs_..."
```

**claude.ai** (Ajustes → Conectores → Añadir conector personalizado): claude.ai no permite cabeceras propias, así que el token va en la URL y el conector se añade sin autenticación:

```
https://ejercicios.tudominio.com/mcp/exs_...
```

Quien tenga esa URL puede subir ejercicios. Para este uso conviene crear tokens de corta duración y revocarlos al terminar.

## Release y despliegue

Cada push a `master` o `main` ejecuta [`.github/workflows/release.yml`](.github/workflows/release.yml), que hace lo siguiente:

1. Ejecuta `go vet` y los tests.
2. Compila `exerciseserver-linux-amd64` estático (sin CGO).
3. Publica la release `v<YYYYMMDDHHMMSS>` con el binario y su `.sha256`.

### Instalación en el servidor (una sola vez)

```bash
curl -fsSLO https://raw.githubusercontent.com/redjhawk/exerciseserver/master/deploy/install.sh
sudo sh install.sh
sudo -u exerciseserver EXERCISESERVER_DATA_DIR=/var/lib/exerciseserver \
  /opt/exerciseserver/bin/exerciseserver admin-password
```

El instalador crea lo siguiente:

| Pieza | Usuario | Función |
|---|---|---|
| `exerciseserver.service` | `exerciseserver` | Ejecuta la app. Los datos están en `/var/lib/exerciseserver`. |
| `exerciseserver-update.timer` y `.service` | `exerciseserver-update` (sin privilegios) | Cada 15 minutos mira la última release. Si es nueva, la descarga, verifica el sha256 y la versión, y la instala en `/opt/exerciseserver/bin`. |
| `exerciseserver-restart.path` y `.service` | root | Cuando cambia `/opt/exerciseserver/bin/.restart`, reinicia el servicio. Este paso no descarga ni procesa nada. |

El script `update.sh` (en `/usr/local/lib/exerciseserver/`) pertenece a root, así que el actualizador no puede modificar su propio código. La app no puede escribir su propio binario.

Para forzar una actualización: `sudo systemctl start exerciseserver-update`. Los logs se ven con `journalctl -u exerciseserver -u exerciseserver-update`.

La configuración opcional va en `/etc/exerciseserver.env` (por ejemplo `EXERCISESERVER_ADDR=0.0.0.0:8090`).

### Cloudflare Tunnel

Añade una regla de ingress a cloudflared:

```yaml
ingress:
  - hostname: ejercicios.tudominio.com
    service: http://localhost:8090
```

Si cloudflared corre en Docker, `localhost` no es el host. En ese caso, usa `network_mode: host` en el contenedor o haz que la app escuche en la IP del bridge (`EXERCISESERVER_ADDR=172.17.0.1:8090`).

## Seguridad

- Las contraseñas se guardan con bcrypt.
- Las sesiones van en una cookie `HttpOnly`, `SameSite=Lax` y `Secure` detrás de HTTPS.
- Todos los formularios de administración llevan token CSRF y se comprueba el origen.
- El login tiene un límite de 10 intentos fallidos cada 15 minutos por IP. La IP se toma de `CF-Connecting-IP`.
- Los ejercicios HTML se sirven con `Content-Security-Policy: sandbox allow-scripts …`. Sus scripts funcionan, pero corren con un origen opaco y no pueden leer la sesión ni llamar a la administración, aunque se abran fuera del iframe.
- Los ficheros nunca se sirven directamente. Siempre pasan por la comprobación de sesión.

## Limitaciones actuales

- Los HTML tienen que ser autocontenidos. Las imágenes o scripts en ficheros aparte no se suben ni se sirven.
- Todos los usuarios ven todos los ficheros.
