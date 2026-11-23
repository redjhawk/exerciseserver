# Instrucciones para crear y publicar ejercicios

Vas a crear ejercicios educativos interactivos para niños y a publicarlos en el servidor de ejercicios a través de su conector MCP («ejercicios»). Los ejercicios pueden usar la IA que el servidor tiene configurada para cada usuario y tienen que funcionar también sin IA. Sigue estas instrucciones en cada ejercicio.

La versión actualizada de este documento está siempre disponible con la herramienta `obtener_instrucciones` del conector. Si tienes acceso a ella, úsala: la copia del servidor prevalece sobre cualquier otra.

## 1. Antes de empezar

1. Comprueba que tienes disponibles las herramientas del servidor: `listar_niveles`, `listar_etiquetas`, `listar_ejercicios`, `leer_ejercicio`, `subir_ejercicio` y `actualizar_ejercicio`.
   - Si no aparecen o devuelven «token no válido, caducado o revocado», díselo al usuario: tiene que crear un token nuevo en **Admin → Tokens** y volver a añadir el conector. Mientras tanto, entrega el HTML como fichero para que lo suba a mano.
2. Llama a `listar_niveles` y a `listar_etiquetas` para conocer los niveles y las etiquetas que ya existen.
3. Llama a `listar_ejercicios` con un `texto` relacionado con el tema (por ejemplo «restas») para comprobar si ya hay un ejercicio parecido.
   - Si existe y el usuario quiere cambiarlo, **no subas uno nuevo**: usa el flujo de actualización (sección 5).

## 2. Cómo debe ser el HTML

- **Un único fichero autocontenido.** HTML, CSS y JavaScript van en línea, sin frameworks, sin CDN, sin fuentes ni imágenes externas. Si necesitas imágenes, usa SVG en línea, emojis o `data:` URIs pequeños.
- **Documento completo**, con `<!doctype html>`, `<html lang="es">`, `<meta charset="utf-8">`, `<meta name="viewport" content="width=device-width, initial-scale=1">` y un `<title>` descriptivo, porque es el título que sale en el catálogo.
- **Se ejecuta dentro de un iframe sandbox** (`allow-scripts allow-forms allow-modals allow-popups allow-downloads`) con origen opaco. Por eso:
  - no uses `fetch`, `XMLHttpRequest` ni `WebSocket`, salvo los de la estrategia de IA de la sección 3: `/api/ia` y las API de los proveedores en el modo de clave propia;
  - `localStorage` puede fallar en el sandbox: úsalo solo a través del objeto `store` de la sección 3, que guarda en memoria si no se puede usar;
  - no uses cookies ni `window.top`, y no navegues fuera de la página.
- **Pensado para niños:**
  - textos claros, en español salvo que se pida otro idioma, adaptados a la edad del nivel;
  - botones y letra grandes, y que funcione con el ratón y en pantalla táctil (tablet);
  - corrección inmediata y amable («¡Muy bien!», «Casi, vuelve a intentarlo»), con un botón para reintentar o generar preguntas nuevas;
  - si tiene sentido, un marcador de aciertos.
- **Adaptable**: tiene que verse bien desde 360 px de ancho hasta un monitor de escritorio.

## 3. Usar la IA (estrategia del catálogo)

Los ejercicios del catálogo pueden usar la IA para corregir respuestas abiertas, dar pistas, generar contenido o revisar fotos. Todos siguen la misma estrategia: **buscan una IA por varias vías, en orden, y si no hay ninguna funcionan sin IA**. Usa siempre esta estrategia, con el código base de esta sección.

### Las vías, en orden

| Vía | Cuándo se usa | Cómo funciona |
|---|---|---|
| 1. **Ventana padre** (`parent`) | El ejercicio se abre desde el catálogo del servidor. Es la vía normal. | Pide la IA a la página del visor con `postMessage`. El servidor usa la IA configurada para el usuario (OpenAI, Gemini o Claude); el ejercicio no ve ninguna clave. |
| 2. **Claude** (`claude`) | El HTML se abre como artifact de Claude. | `window.claude.use("sample")`. |
| 3. **Servidor del mismo origen** (`server`) | El HTML se sirve desde un servidor que publica `/api/ia` en su mismo origen. | `fetch('/api/ia')` con la sesión del usuario. |
| 4. **Clave propia** (`key`) | Ninguna de las anteriores: el fichero se ha descargado, se ha abierto con «Abrir aparte», etc. | El usuario pega su clave de Gemini, Claude o ChatGPT en el panel **⚙ IA**. Se guarda solo en su navegador y el ejercicio llama directamente al proveedor. |
| 5. **Sin IA** | No hay ninguna vía disponible, o la IA falla. | El ejercicio corrige en local con reglas estrictas. |

### Funcionar sin IA

El ejercicio **siempre** tiene que poder hacerse sin IA. Lo hacen así:

1. **Corrección local estricta.** Cada pregunta tiene una función `localCheck(respuesta)` que acepta la respuesta exacta, o sus variantes normalizadas: sin mayúsculas, sin tildes, sin signos de puntuación y sin espacios repetidos.
2. **Primero la IA y, si falla, la corrección local.** Al pulsar «Comprobar»:
   - si la IA responde, se usa su veredicto y su comentario;
   - si la IA no está disponible o da cualquier error, se aplica `localCheck`. Si acierta, se marca como correcta; si no, se muestra el mensaje de error de la IA y, en su caso, se abre el panel ⚙ IA.
3. **Explicar el modo sin IA.** Debajo del campo de respuesta hay una nota que cambia según la vía:
   - padre o servidor: «La IA de tu servidor corrige tu respuesta.»;
   - clave propia: «Gemini corrige tu respuesta con tu clave.»;
   - sin IA: «Sin IA, solo se acepta la respuesta exacta. Configúrala con el botón ⚙ IA.».
4. **Botón «Ver solución»** siempre visible, para que el niño no se quede bloqueado si no hay IA.
5. **Lo que solo tiene sentido con IA** (pistas, comentarios a una redacción, revisar una foto) se oculta o se desactiva si `IA.available()` es falso, en vez de dar error.

Si el ejercicio se corrige del todo con lógica normal (sumas, test con respuesta fija), corrígelo en JavaScript y usa la IA, como mucho, para pistas.

### Reglas

1. **En la vía padre, el proveedor es el que dice el servidor.** Usa `defaultProvider` de `ia:config` (o el primero de `providers`), o no envíes `provider`. Nunca fijes `'openai'` ni otro: el servidor rechaza con `unsupported_provider` cualquier proveedor distinto del configurado. No envíes `model`.
2. **No bloquees la carga.** Arranca la detección (`IA.init()`) al cargar la página, pero deja que el ejercicio se use mientras tanto. La detección dura como mucho unos 3 segundos.
3. **Pide la IA solo cuando el niño actúe** (al pulsar un botón), nunca en bucle. En el servidor hay un límite de **30 peticiones por hora y usuario**. Desactiva el botón mientras esperas («Comprobando…»).
4. **Pide JSON cuando necesites un veredicto** (`{"correct": true, "comment": "…"}`) y lee la respuesta con `parseJSON`, que tolera bloques de código Markdown y texto alrededor. Si no se puede interpretar, trátalo como el error `invalid_json`.
5. **Muestra el texto de la IA con `textContent`**, nunca con `innerHTML`.
6. **Escribe `system` y `prompt` pensando en niños:** indica la edad o el curso, pide frases cortas y tono amable y, en las pistas, que no se dé la solución.
7. **Las claves propias solo se guardan en el navegador del usuario** (a través de `store`). Nunca se escriben en el HTML ni se envían a otro sitio que no sea la API del proveedor.

### Protocolo de la vía padre

**Detección.** El ejercicio envía este mensaje al cargar, otra vez a los 500 ms y otra a los 1500 ms. Si a los 3 s no hay respuesta, pasa a la vía siguiente.

```javascript
window.parent.postMessage({ type: 'ia:hello', requestId }, '*');
```

Respuesta (compruébala con `event.source === window.parent`):

```javascript
{ source: 'exerciseserver', type: 'ia:config', requestId, ok: true,
  providers: ['gemini'],      // vacío [] = el usuario no tiene IA → motivo "no_config"
  defaultProvider: 'gemini',
  images: true, maxImages: 3, version: 1 }
// si la sesión ha caducado: ok: false, code: 'unauthenticated'
```

**Petición:**

```javascript
window.parent.postMessage({ type: 'ia:request', requestId, payload: {
  prompt: '…',                 // obligatorio
  system: '…',                 // opcional
  provider: cfg.defaultProvider, // opcional; si lo pones, el de ia:config
  temperature: 0.2,            // opcional: 0–1
  json: true,                  // opcional: responde solo con JSON
  images: [{ mimeType: 'image/jpeg', data: 'BASE64_SIN_PREFIJO' }] // opcional: hasta 3
} }, '*');
```

Respuesta:

```javascript
{ source: 'exerciseserver', type: 'exerciseserver:ia:result', requestId, ok: true, data: { text: '…' } }
{ source: 'exerciseserver', type: 'exerciseserver:ia:result', requestId, ok: false, code: 'rate_limited', error: '…' }
```

Usa un tiempo de espera de 150 s para la petición.

Códigos de error y mensaje para el niño:

| Código | Mensaje |
|---|---|
| `no_config` | No hay ninguna IA configurada para tu cuenta. |
| `unauthenticated` | Tu sesión ha caducado. Vuelve a entrar. |
| `rate_limited` | Has hecho muchas consultas. Espera un poco. |
| `timeout` | La IA tarda demasiado. Vuelve a intentarlo. |
| `ai_error`, `request_failed` | La IA no ha respondido. |
| `invalid_json` | La respuesta de la IA llegó incompleta. Vuelve a intentarlo. |
| `too_large` | La petición es demasiado grande. |
| `invalid_image` | La imagen no es válida. |
| `no_key` | No hay ninguna IA configurada: añade una clave en ⚙ IA. |
| `bad_key` | La clave no es válida. |
| `quota`, `no_credit` | La clave no tiene saldo o ha superado su límite. |
| `bad_model` | Ese modelo no existe para esta clave. |
| `not_granted` | No se ha permitido usar Claude. |

Añade siempre detrás, si corresponde, «Sin IA, solo se acepta la respuesta exacta».

### Código base (cópialo tal cual)

Pon este bloque al principio de tu `<script>`. Expone `IA.init()`, `IA.available()`, `IA.unavailableCode()`, `IA.via`, `IA.ask(prompt, { system, temperature, json, images })`, `IA.saveKey(proveedor, clave)`, `IA.clearKey()` y las funciones `parseJSON(texto)` y `store`.

```javascript
// ---------- IA: estrategia del catálogo (padre → Claude → servidor → clave → sin IA) ----------
const IA_DEBUG = false;
const iaLog = (...a) => { if (IA_DEBUG) console.log('[IA]', ...a); };
const rid = () => (window.crypto && crypto.randomUUID) ? crypto.randomUUID() : 'r' + Date.now().toString(36) + Math.random().toString(36).slice(2);

// localStorage si se puede; si no (sandbox), memoria.
const store = {
  mem: {},
  get(k) { try { const v = localStorage.getItem(k); return v !== null ? v : (k in this.mem ? this.mem[k] : null); } catch (e) { return k in this.mem ? this.mem[k] : null; } },
  set(k, v) { this.mem[k] = v; try { localStorage.setItem(k, v); } catch (e) {} },
  del(k) { delete this.mem[k]; try { localStorage.removeItem(k); } catch (e) {} },
};

function parseJSON(t) {
  if (t == null) return null;
  t = String(t).trim();
  try { return JSON.parse(t); } catch (e) {}
  const f = t.match(/`{3}(?:json)?\s*([\s\S]*?)`{3}/);
  if (f) { try { return JSON.parse(f[1]); } catch (e) {} }
  const a = t.indexOf('{'), b = t.lastIndexOf('}');
  if (a >= 0 && b > a) { try { return JSON.parse(t.slice(a, b + 1)); } catch (e) {} }
  return null;
}

const IA = (() => {
  const KEY_PROVIDERS = {
    gemini: { label: 'Google Gemini', url: 'https://aistudio.google.com/app/apikey', def: 'gemini-2.5-flash' },
    claude: { label: 'Claude', url: 'https://console.anthropic.com/settings/keys', def: 'claude-haiku-5-5' },
    openai: { label: 'ChatGPT', url: 'https://platform.openai.com/api-keys', def: 'gpt-4o-mini' },
  };
  const st = { via: 'detecting', parent: null, sample: null, server: null, denied: false, readyP: null,
               keyProv: KEY_PROVIDERS[store.get('ia_provider')] ? store.get('ia_provider') : 'gemini' };
  const TIMEOUT = 150000;
  const provIds = (list) => (Array.isArray(list) ? list : []).map((p) => typeof p === 'string' ? p : p && (p.id || p.name)).filter(Boolean);

  // Vía 1: ventana padre
  function discoverParent() {
    return new Promise((resolve) => {
      if (window.parent === window) return resolve(null);
      const requestId = rid(); let done = false; const timers = [];
      const finish = (v) => { if (done) return; done = true; window.removeEventListener('message', onMsg); timers.forEach(clearTimeout); resolve(v); };
      const onMsg = (ev) => {
        if (ev.source !== window.parent) return;
        const d = ev.data;
        if (!d || d.type !== 'ia:config' || (d.requestId && d.requestId !== requestId)) return;
        iaLog('ia:config', d); finish(d);
      };
      window.addEventListener('message', onMsg);
      const send = () => { if (!done) try { window.parent.postMessage({ type: 'ia:hello', requestId }, '*'); } catch (e) {} };
      send(); timers.push(setTimeout(send, 500), setTimeout(send, 1500), setTimeout(() => finish(null), 3000));
    });
  }
  function applyParentConfig(cfg) {
    st.parent = { provider: null, reason: null, images: !!cfg.images };
    if (cfg.ok === false && (cfg.code === 'unauthenticated' || cfg.status === 401 || cfg.status === 403)) { st.parent.reason = 'unauthenticated'; return; }
    const ids = provIds(cfg.providers);
    if (!ids.length) { st.parent.reason = 'no_config'; return; }
    st.parent.provider = ids.includes(cfg.defaultProvider) ? cfg.defaultProvider : ids[0];
  }
  function askParent(payload) {
    return new Promise((resolve, reject) => {
      const requestId = rid();
      const cleanup = () => { window.removeEventListener('message', onMsg); clearTimeout(tm); };
      const onMsg = (ev) => {
        if (ev.source !== window.parent) return;
        const d = ev.data || {};
        if (d.type !== 'exerciseserver:ia:result' || d.requestId !== requestId) return;
        cleanup();
        if (d.ok === false) reject({ code: d.code || 'ai_error', error: d.error });
        else resolve(d.data && d.data.text != null ? d.data.text : '');
      };
      const tm = setTimeout(() => { cleanup(); reject({ code: 'timeout' }); }, TIMEOUT);
      window.addEventListener('message', onMsg);
      window.parent.postMessage({ type: 'ia:request', requestId, payload }, '*');
    });
  }

  // Vía 2: Claude (artifact)
  async function askClaude(system, prompt) {
    try { return (await st.sample((system ? system + '\n\n' : '') + prompt, { modelTier: 'quick' })).text; }
    catch (e) {
      const c = e && e.code;
      if (['not_granted', 'sampling_disabled', 'not_declared', 'capability_disabled', 'capability_removed'].includes(c)) { st.denied = true; throw { code: 'not_granted' }; }
      throw { code: c === 'rate_limited' ? 'rate_limited' : 'ai_error', error: e && e.message };
    }
  }

  // Vía 3: servidor del mismo origen
  async function readJson(r) { try { return await r.json(); } catch (e) { return null; } }
  async function tfetch(url, opts) {
    const ctl = new AbortController(); const tm = setTimeout(() => ctl.abort(), TIMEOUT);
    try { return await fetch(url, Object.assign({}, opts, { signal: ctl.signal })); }
    catch (e) { throw { code: e.name === 'AbortError' ? 'timeout' : 'request_failed', error: e.message }; }
    finally { clearTimeout(tm); }
  }
  async function detectServer() {
    try {
      const r = await fetch('/api/ia/config', { credentials: 'same-origin', headers: { Accept: 'application/json' }, redirect: 'manual' });
      const ct = (r.headers.get('content-type') || '').toLowerCase();
      if (r.type === 'opaqueredirect' || r.status === 401 || r.status === 403) return { loggedIn: false, providers: [] };
      if (r.ok && ct.includes('json')) { const c = (await readJson(r)) || {}; return { loggedIn: true, providers: provIds(c.providers), defaultProvider: c.defaultProvider, images: !!c.images }; }
    } catch (e) {}
    return null;
  }
  async function askServer(body) {
    const r = await tfetch('/api/ia', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify(body) });
    if (r.status === 401 || r.status === 403 || r.redirected) { st.server.loggedIn = false; throw { code: 'unauthenticated' }; }
    const data = await readJson(r);
    if (!r.ok || !data || data.ok === false) throw { code: (data && data.code) || (r.status === 429 ? 'rate_limited' : 'ai_error'), error: data && data.error };
    return data.text;
  }

  // Vía 4: clave propia en el navegador
  function keyError(status, data) {
    const msg = JSON.stringify(data || {}).toLowerCase();
    if (status === 401 || status === 403 || /api_key_invalid|api key not valid|invalid api key|invalid x-api-key|incorrect api key|authentication_error/.test(msg)) return { code: 'bad_key' };
    if (/credit balance|insufficient_quota|billing/.test(msg)) return { code: 'no_credit' };
    if (status === 429) return { code: 'quota' };
    if (status === 404 || /not_found_error|model.{0,40}(not found|does not exist|is not supported)/.test(msg)) return { code: 'bad_model' };
    const e = data && data.error;
    return { code: 'ai_error', error: (e && (e.message || (typeof e === 'string' ? e : ''))) || ('HTTP ' + status) };
  }
  async function askKey({ system, prompt, temperature, json, images }) {
    const p = st.keyProv, key = store.get('key_' + p), model = store.get('ia_model_' + p) || KEY_PROVIDERS[p].def;
    let r, data;
    if (p === 'gemini') {
      const parts = [{ text: prompt }].concat((images || []).map((i) => ({ inline_data: { mime_type: i.mimeType, data: i.data } })));
      const gen = { temperature }; if (json) gen.responseMimeType = 'application/json';
      r = await tfetch(`https://generativelanguage.googleapis.com/v1beta/models/${encodeURIComponent(model)}:generateContent?key=${encodeURIComponent(key)}`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ systemInstruction: system ? { parts: [{ text: system }] } : undefined, contents: [{ role: 'user', parts }], generationConfig: gen }) });
      data = await readJson(r); if (!r.ok) throw keyError(r.status, data);
      const c = data && data.candidates && data.candidates[0];
      return ((c && c.content && c.content.parts) || []).map((x) => x.text || '').join('');
    }
    if (p === 'claude') {
      const content = (images || []).map((i) => ({ type: 'image', source: { type: 'base64', media_type: i.mimeType, data: i.data } })).concat([{ type: 'text', text: prompt }]);
      r = await tfetch('https://api.anthropic.com/v1/messages', {
        method: 'POST', headers: { 'Content-Type': 'application/json', 'x-api-key': key, 'anthropic-version': '2023-06-01', 'anthropic-dangerous-direct-browser-access': 'true' },
        body: JSON.stringify({ model, max_tokens: 1024, system: system || undefined, temperature, messages: [{ role: 'user', content }] }) });
      data = await readJson(r); if (!r.ok) throw keyError(r.status, data);
      return ((data && data.content) || []).filter((c) => c.type === 'text').map((c) => c.text).join('');
    }
    const userContent = images && images.length
      ? [{ type: 'text', text: prompt }].concat(images.map((i) => ({ type: 'image_url', image_url: { url: `data:${i.mimeType};base64,${i.data}` } })))
      : prompt;
    const body = { model, temperature, messages: (system ? [{ role: 'system', content: system }] : []).concat([{ role: 'user', content: userContent }]) };
    if (json) body.response_format = { type: 'json_object' };
    const call = () => tfetch('https://api.openai.com/v1/chat/completions', { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + key }, body: JSON.stringify(body) });
    r = await call(); data = await readJson(r);
    if (r.status === 400 && /temperature/i.test(JSON.stringify(data || {}))) { delete body.temperature; r = await call(); data = await readJson(r); }
    if (!r.ok) throw keyError(r.status, data);
    const ch = data && data.choices && data.choices[0];
    return (ch && ch.message && ch.message.content) || '';
  }

  // Estado común
  function available() {
    switch (st.via) {
      case 'parent': return !st.parent.reason;
      case 'claude': return !st.denied;
      case 'server': return st.server.loggedIn && st.server.providers.length > 0;
      case 'key': return !!store.get('key_' + st.keyProv);
      default: return false;
    }
  }
  function unavailableCode() {
    switch (st.via) {
      case 'parent': return st.parent.reason || 'no_config';
      case 'claude': return 'not_granted';
      case 'server': return st.server.loggedIn ? 'no_config' : 'unauthenticated';
      default: return 'no_key';
    }
  }
  function imagesSupported() {
    return (st.via === 'parent' && st.parent.images) || (st.via === 'server' && st.server.images) || st.via === 'key';
  }

  function init() {
    if (st.readyP) return st.readyP;
    st.readyP = (async () => {
      const cfg = await discoverParent();
      if (cfg) { applyParentConfig(cfg); return finish('parent'); }
      if (window.claude && typeof window.claude.use === 'function') {
        try { st.sample = await window.claude.use('sample'); } catch (e) {}
        if (st.sample) return finish('claude');
      }
      if (!window.claude && /^https?:$/.test(location.protocol)) {
        const srv = await detectServer();
        if (srv) { st.server = srv; return finish('server'); }
      }
      return finish('key');
    })();
    return st.readyP;
  }
  function finish(via) { st.via = via; iaLog('vía elegida:', via); document.dispatchEvent(new CustomEvent('ia:ready', { detail: via })); return via; }

  async function ask(prompt, { system = '', temperature = 0.3, json = false, images } = {}) {
    await init();
    if (!available()) throw { code: unavailableCode() };
    try {
      if (st.via === 'parent') {
        const payload = { prompt, system, temperature, json };
        if (st.parent.provider) payload.provider = st.parent.provider;
        if (images && images.length) payload.images = images;
        return await askParent(payload);
      }
      if (st.via === 'claude') return await askClaude(system, prompt);
      if (st.via === 'server') {
        const body = { prompt, system, temperature, json };
        if (images && images.length) body.images = images;
        return await askServer(body);
      }
      return await askKey({ system, prompt, temperature, json, images });
    } catch (e) {
      throw e && e.code ? e : { code: 'request_failed', error: String((e && e.message) || e) };
    }
  }

  function saveKey(provider, key) {
    if (!KEY_PROVIDERS[provider]) return;
    st.keyProv = provider; store.set('ia_provider', provider); store.set('key_' + provider, String(key || '').trim());
  }
  function clearKey() { store.del('key_' + st.keyProv); }

  return {
    init, ask, available, unavailableCode, imagesSupported, saveKey, clearKey, KEY_PROVIDERS,
    get via() { return st.via; }, get keyProvider() { return st.keyProv; },
  };
})();
```

### Interfaz mínima de IA

Todos los ejercicios con IA llevan:

- Un **botón «⚙ IA»** con el estado en su texto: «⚙ IA · servidor ✓», «⚙ IA · servidor · sin IA», «⚙ IA · Claude ✓», «⚙ IA · clave ✓» o «⚙ IA · sin clave».
- Un **panel plegable** que se abre con ese botón. En la vía padre, la de servidor y la de Claude solo muestra un texto: «Tu servidor proporciona la IA: no hace falta ninguna clave». En la vía de clave propia tiene:
  - un selector de proveedor (Google Gemini, con clave gratuita; Claude; ChatGPT);
  - un campo de clave de tipo `password`;
  - los botones «Guardar» y «Borrar la clave»;
  - un enlace para crear la clave (`IA.KEY_PROVIDERS[p].url`) con `target="_blank"`. Dentro del iframe, las páginas de inicio de sesión no se abren, así que ofrece también copiar la dirección.
- La **nota del modo** bajo la respuesta («La IA de tu servidor corrige tu respuesta» / «Sin IA, solo se acepta la respuesta exacta…»).
- Que el panel se **abra solo** cuando un error lo requiera (`no_key`, `bad_key`, `no_credit`, `quota`, `bad_model`) en las vías de clave propia o de servidor.

Actualiza el botón y la nota cuando se dispare el evento `ia:ready`, y también después de guardar o borrar una clave.

### Ejemplo: comprobar una respuesta, con IA o sin ella

```javascript
const MSG = {
  no_config: 'No hay ninguna IA configurada para tu cuenta.', unauthenticated: 'Tu sesión ha caducado. Vuelve a entrar.',
  rate_limited: 'Has hecho muchas consultas. Espera un poco.', timeout: 'La IA tarda demasiado. Vuelve a intentarlo.',
  invalid_json: 'La respuesta de la IA llegó incompleta. Vuelve a intentarlo.', no_key: 'No hay ninguna IA configurada: añade una clave en ⚙ IA.',
  bad_key: 'La clave no es válida.', quota: 'La clave ha superado su límite.', no_credit: 'La clave no tiene saldo.',
  bad_model: 'Ese modelo no existe para esta clave.', not_granted: 'No se ha permitido usar Claude.', ai_error: 'La IA no ha respondido.',
};
const errText = (code) => (MSG[code] || MSG.ai_error) + ' Sin IA, solo se acepta la respuesta exacta.';

// Normaliza para comparar sin mayúsculas, tildes, puntuación ni espacios repetidos.
const norm = (s) => String(s).toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '')
  .replace(/[^\p{L}\p{N}\s]/gu, ' ').replace(/\s+/g, ' ').trim();
const localCheck = (respuesta, pregunta) => pregunta.aceptadas.some((a) => norm(a) === norm(respuesta));

IA.init(); // arranca la detección sin esperar

async function comprobar() {
  const respuesta = $('respuesta').value.trim();
  if (!respuesta) return mostrar('vacía');
  const btn = $('comprobar');
  btn.disabled = true; btn.textContent = 'Comprobando…';
  try {
    await IA.init();
    if (!IA.available()) throw { code: IA.unavailableCode() };
    const res = parseJSON(await IA.ask(
      `Pregunta: ${pregunta.texto}\nRespuesta correcta: ${pregunta.aceptadas[0]}\nRespuesta del niño: """${respuesta}"""\n` +
      'Decide si la respuesta del niño es correcta aunque esté escrita de otra forma. ' +
      'Devuelve solo {"correct": true|false, "comment": "una frase corta y amable para un niño de 8 años, sin dar la solución si es incorrecta"}.',
      { system: 'Eres un profesor de 3º de Primaria. Respondes solo con JSON válido.', temperature: 0.2, json: true }));
    if (!res || typeof res !== 'object') throw { code: 'invalid_json' };
    const ok = res.correct === true || res.correct === 'true';
    mostrar(ok ? 'bien' : 'mal', typeof res.comment === 'string' ? res.comment : '');
  } catch (e) {
    // Sin IA o con error: corrección local estricta.
    if (localCheck(respuesta, pregunta)) mostrar('bien', '');
    else mostrar('error', errText(e && e.code));
  } finally {
    btn.disabled = false; btn.textContent = 'Comprobar';
  }
}
```

`mostrar` escribe los mensajes con `textContent`. El botón «Ver solución» muestra `pregunta.aceptadas[0]`.

### Ejemplo: pista (solo con IA)

```javascript
document.addEventListener('ia:ready', () => { $('pista').hidden = !IA.available(); });

$('pista').addEventListener('click', async () => {
  const btn = $('pista'); btn.disabled = true; $('ayuda').textContent = 'Pensando…';
  try {
    $('ayuda').textContent = await IA.ask(
      `El niño tiene que resolver: ${pregunta.texto}. Ha respondido: "${$('respuesta').value || 'nada todavía'}". Dale una pista para que llegue él solo.`,
      { system: 'Eres un profesor paciente de 2º de Primaria. Una o dos frases cortas y amables. Nunca des la solución.', temperature: 0.4 });
  } catch (e) {
    $('ayuda').textContent = (MSG[e && e.code] || MSG.ai_error);
  } finally { btn.disabled = false; }
});
```

### Ejemplo: foto

Muestra el selector solo si `IA.available() && IA.imagesSupported()`. Reduce la imagen antes de enviarla:

```javascript
function imagenDesdeFichero(file, maxLado = 1600) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const k = Math.min(1, maxLado / Math.max(img.width, img.height));
      const c = document.createElement('canvas');
      c.width = Math.round(img.width * k); c.height = Math.round(img.height * k);
      c.getContext('2d').drawImage(img, 0, 0, c.width, c.height);
      URL.revokeObjectURL(img.src);
      resolve({ mimeType: 'image/jpeg', data: c.toDataURL('image/jpeg', 0.85).split(',')[1] });
    };
    img.onerror = () => reject(new Error('No se ha podido leer la imagen.'));
    img.src = URL.createObjectURL(file);
  });
}
// const texto = await IA.ask('Revisa estas sumas hechas a mano…', { system: '…', images: [await imagenDesdeFichero(file)] });
```

## 4. Publicar un ejercicio nuevo

Llama a `subir_ejercicio` con estos argumentos:

| Argumento | Obligatorio | Cómo rellenarlo |
|---|---|---|
| `nombre_fichero` | Sí | En minúsculas, con guiones y sin espacios: `restas-sin-llevar-2-primaria.html`. |
| `html` | Sí | El documento completo. |
| `titulo` | No | Título corto y claro para el catálogo. Si lo omites, se usa el `<title>`. |
| `descripcion` | No, pero ponla | Una o dos frases: qué practica el niño y cómo funciona. Si usa IA, dilo («incluye pistas con IA»). |
| `nivel` | No | El nombre **exacto** de uno de los niveles de `listar_niveles`. No inventes niveles; si ninguno encaja, omítelo y avisa al usuario. |
| `etiquetas` | No | De 2 a 4 etiquetas. Reutiliza las de `listar_etiquetas` (por ejemplo «matemáticas», «restas»); crea una nueva solo si ninguna encaja. Si usa IA, añade la etiqueta «IA». |

Ejemplo:

```json
{
  "nombre_fichero": "restas-sin-llevar-2-primaria.html",
  "html": "<!doctype html><html lang=\"es\">…</html>",
  "titulo": "Restas sin llevar",
  "descripcion": "10 restas de dos cifras sin llevar, con corrección al momento y pistas con IA.",
  "nivel": "2º Primaria",
  "etiquetas": ["matemáticas", "restas", "IA"]
}
```

La herramienta devuelve el enlace del ejercicio (`…/ver/{id}`). **Dale siempre el enlace al usuario.** Si contesta que ya existía uno idéntico, no lo ha duplicado: dale ese enlace.

Si devuelve un error, léelo y corrige la llamada. Por ejemplo, si el nivel no existe, el error incluye la lista de niveles válidos. No reintentes la misma llamada sin cambios.

## 5. Modificar un ejercicio que ya existe

1. Búscalo con `listar_ejercicios` y apunta su `id`. Si hay varios candidatos, pregunta al usuario cuál es.
2. Léelo con `leer_ejercicio(id)` y **parte de su HTML actual**; no lo reescribas desde cero salvo que te lo pidan.
   - Si usa la IA con un código antiguo (por ejemplo, con `provider: 'openai'` fijo o sin modo sin IA), cámbialo por el código base de la sección 3.
3. Guárdalo con `actualizar_ejercicio`, enviando **solo los campos que cambian**:
   - para cambiar el contenido: `{"id": 12, "html": "…documento completo…"}`;
   - para cambiar datos del catálogo: `{"id": 12, "titulo": "…", "etiquetas": ["…"]}`;
   - `nivel: ""` deja el ejercicio sin nivel y `etiquetas: []` le quita todas. Las etiquetas que envíes **sustituyen** a las actuales, así que incluye también las que quieras conservar.
4. El id y el enlace no cambian. Dale el enlace al usuario.

Actualizar sustituye el HTML anterior y no guarda una copia. Si el cambio es grande o arriesgado, confírmalo antes con el usuario.

## 6. Qué no puedes hacer

- No puedes borrar ejercicios. Si el usuario quiere borrar uno, dile que lo haga desde **Admin → Ficheros**.
- No puedes crear ni modificar niveles: se gestionan en **Admin → Niveles**.
- No puedes configurar la IA de los usuarios: se hace en **Admin → Usuarios** o en la página **IA** de cada usuario.
- Los PDF se pueden listar y consultar, pero no puedes leer ni cambiar su contenido.

## 7. Resumen del flujo

```
listar_niveles + listar_etiquetas + listar_ejercicios(texto)
        │
        ├─ ¿Ya existe y hay que cambiarlo? → leer_ejercicio → actualizar_ejercicio
        │
        └─ ¿Es nuevo? → generar el HTML (sección 2, y sección 3 si usa IA) → subir_ejercicio
        │
        └─ Dar al usuario el enlace /ver/{id}
```

Antes de subir un ejercicio que usa IA, comprueba que:
- [ ] usa el código base de la sección 3 (padre → Claude → servidor → clave → sin IA);
- [ ] en la vía padre no fija `provider` (usa `defaultProvider`) ni `model`;
- [ ] **se puede hacer entero sin IA**: corrección local estricta, nota del modo sin IA y botón «Ver solución»;
- [ ] lo que solo funciona con IA (pistas, fotos) se oculta si `IA.available()` es falso;
- [ ] tiene el botón ⚙ IA con su estado y el panel para la clave propia;
- [ ] el botón se desactiva mientras espera la respuesta y los errores se explican con frases sencillas;
- [ ] el texto de la IA se muestra con `textContent`.
