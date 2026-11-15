# Guía para generar ejercicios con IA

Esta guía sirve para que una IA genere ejercicios pedagógicos para este proyecto sin romper el sandbox, sin exponer claves ni tokens, y sin depender de llamadas directas a la API del navegador.

## 1. Regla principal

Los ejercicios no deben llamar a APIs desde dentro del iframe. El iframe solo debe enviar mensajes a la ventana padre y la ventana principal es la que realiza la petición autenticada al servidor.

Esto evita dos problemas reales:
- `Origin: null` o `opaque origin` cuando un ejercicio se ejecuta dentro de un iframe sandboxado.
- `401 Unauthorized` si la petición se hace sin la sesión del usuario.

La solución correcta es:
- el ejercicio usa `window.parent.postMessage(...)`
- la ventana padre escucha ese mensaje
- la ventana padre hace la llamada a `/api/ia` o `/api/ia/config` usando la sesión del usuario
- la respuesta del servidor vuelve al iframe por `postMessage`

## 2. Requisitos de seguridad

Todo ejercicio generado por IA debe cumplir esto:

- HTML puro, sin frameworks externos.
- Sin `fetch`, `XMLHttpRequest`, `WebSocket`, ni llamadas a APIs desde el ejercicio.
- Sin bibliotecas CDN o dependencias externas.
- Sin acceso a cookies ni sesión del usuario.
- Sin exposición de claves ni tokens.
- Sin supuestos sobre el origen del navegador.
- Compatible con iframe sandbox con JavaScript habilitado.
- Debe funcionar sin internet, salvo cuando el usuario pida ayuda de IA y la ventana padre lo permita.

## 3. Protocolo de mensajes seguro

El proyecto acepta dos familias de mensajes:

- formato clásico: `exerciseserver:*`
- formato simplificado: `ia:*`

Para ejercicios nuevos, se recomienda usar `ia:*` porque es más limpio y más fácil de documentar.

### 3.1. Petición de configuración de IA

Desde el iframe:

```javascript
const requestId = crypto.randomUUID ? crypto.randomUUID() : String(Date.now());
window.parent.postMessage({
  type: 'ia:hello',
  requestId
}, '*');
```

La ventana padre responde con un mensaje del tipo:

```javascript
{
  type: 'ia:config',
  requestId: '...',
  ok: true,
  providers: ['openai','gemini','claude'],
  defaultProvider: 'openai',
  images: false,
  maxImages: 3,
  version: 1
}
```

También se acepta:

```javascript
window.parent.postMessage({
  type: 'ia:config',
  requestId
}, '*');
```

### 3.2. Petición de IA

Desde el iframe se envía un `payload` completo. La ventana padre no lo modifica ni lo corta: hace exactamente `JSON.stringify(payload)` y lo manda al backend.

Esto significa que los campos disponibles son los que el ejercicio haya incluido:

```javascript
const requestId = crypto.randomUUID ? crypto.randomUUID() : String(Date.now());
window.parent.postMessage({
  type: 'ia:request',
  requestId,
  payload: {
    prompt: 'Explica esta pregunta paso a paso para un niño de 8 años.',
    system: 'Eres un profesor paciente que explica para niños de 8 años.',
    provider: 'openai',
    model: 'gpt-4o-mini',
    temperature: 0.4,
    images: [
      { mimeType: 'image/jpeg', data: 'BASE64_DE_LA_IMAGEN' }
    ],
    json: false
  }
}, '*');
```

Si el payload solo incluye `prompt`, `provider` y `model`, entonces el backend solo recibirá esos tres campos. El dictado con texto funcionará, pero funciones con imagen no, porque `images` debe estar presente y ser válido.

La respuesta esperada por el iframe es:

```javascript
{
  type: 'exerciseserver:ia:result',
  requestId: '...',
  ok: true,
  data: {
    text: 'Respuesta generada por el servidor...'
  }
}
```

Si la petición falla, la respuesta será algo como:

```javascript
{
  type: 'exerciseserver:ia:result',
  requestId: '...',
  ok: false,
  code: 'request_failed',
  error: 'No se pudo consultar la IA'
}
```

## 4. Ejemplo de implementación real en un ejercicio

```html
<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8" />
  <title>Ejemplo con IA</title>
  <style>
    body {
      font-family: Arial, sans-serif;
      background: #f4f9ff;
      color: #1f2a37;
      margin: 0;
      padding: 24px;
    }
    .card {
      max-width: 760px;
      margin: 0 auto;
      background: white;
      border-radius: 16px;
      padding: 24px;
      box-shadow: 0 6px 20px rgba(0,0,0,0.08);
    }
    button {
      background: #2563eb;
      color: white;
      border: none;
      border-radius: 10px;
      padding: 10px 18px;
      font-size: 16px;
      cursor: pointer;
    }
    #feedback {
      margin-top: 16px;
      padding: 12px;
      background: #eef6ff;
      border-radius: 10px;
      display: none;
    }
  </style>
</head>
<body>
  <div class="card">
    <h1>Ayuda de IA</h1>
    <p>Haz clic para pedir una pista adaptada a tu nivel.</p>
    <button id="askBtn">Pedir pista</button>
    <div id="feedback"></div>
  </div>

  <script>
    const askBtn = document.getElementById('askBtn');
    const feedback = document.getElementById('feedback');

    function sendToParent(type, payload = {}, requestId = String(Date.now())) {
      window.parent.postMessage({ type, requestId, payload }, '*');
    }

    function showMessage(text, ok = true) {
      feedback.style.display = 'block';
      feedback.style.background = ok ? '#eefaf1' : '#fbe8e8';
      feedback.textContent = text;
    }

    window.addEventListener('message', (event) => {
      const data = event.data;
      if (!data || typeof data !== 'object') return;

      if (data.type === 'ia:config' && data.ok) {
        console.log('Configuración disponible:', data);
      }

      if (data.type === 'exerciseserver:ia:result') {
        if (!data.ok) {
          showMessage(data.error || 'No se pudo obtener la ayuda.', false);
          return;
        }

        const text = data.data && data.data.text ? data.data.text : 'Sin respuesta.';
        showMessage(text, true);
      }
    });

    askBtn.addEventListener('click', () => {
      sendToParent('ia:request', {
        prompt: 'Explica con palabras sencillas cómo resolver esta actividad para un niño de 8 años.',
        provider: 'openai',
        model: 'gpt-4o-mini'
      });
      showMessage('Solicitando ayuda...', true);
    });

    sendToParent('ia:hello');
  </script>
</body>
</html>
```

## 5. Regla de diseño para cualquier tipo de ejercicio

La IA puede usarse para cualquier tipo de ejercicio, siempre que la lógica del ejercicio siga esta estructura:

- parte visual del ejercicio en HTML/CSS/JS
- la pregunta o el reto se muestra al alumno
- el alumno interactúa con el ejercicio
- si necesita ayuda, el iframe pide a la ventana padre
- la ventana padre usa la sesión del usuario para responder de forma segura
- el resultado es devuelto al iframe y mostrado sin salir del sandbox

### Tipos de ejercicio compatibles

- cálculo
- comprensión lectora
- selección múltiple
- completar huecos
- emparejamiento
- ejercicios de vocabulario
- series
- lógica y razonamiento
- ejercicios abiertos con pista guiada
- creación de actividades personalizadas

## 6. Regla de payload: qué se reenvía exactamente

La ventana padre no limita ni recorta la petición por defecto. Reenvía el payload que recibe del iframe tal cual al backend, con este flujo:

```text
iframe -> window.parent.postMessage({ type: 'ia:request', payload: {...} }, '*')
      -> parent escucha y hace fetch('/api/ia', { body: JSON.stringify(payload) })
      -> backend recibe exactamente esos campos
```

Eso quiere decir que:

- `prompt` se reenvía si está presente
- `provider` se reenvía si está presente
- `model` se reenvía si está presente
- `system` se reenvía si está presente
- `temperature` se reenvía si está presente
- `images` se reenvía si está presente
- `json` se reenvía si está presente

Si un ejercicio genera un payload minimalista como este:

```javascript
{
  prompt: 'Explica esta imagen',
  provider: 'openai',
  model: 'gpt-4o-mini'
}
```

entonces el backend solo recibirá esos campos. La IA podrá responder texto, pero no podrá analizar imágenes porque `images` no existe.

Para uso con foto o multimodal, el payload debe incluir explícitamente:

```javascript
{
  prompt: '¿Qué ves en esta imagen?',
  provider: 'openai',
  model: 'gpt-4o-mini',
  images: [{ mimeType: 'image/jpeg', data: 'BASE64...' }]
}
```

## 7. Ejemplo concreto de ejercicio completo

Este es un ejemplo listo para copiar y adaptar a cualquier ejercicio del catálogo. Sirve para un ejercicio de lectura o explicación, pero puedes cambiar la lógica y el contenido sin tocar la parte de IA.

```html
<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8" />
  <title>Comprensión lectora con ayuda de IA</title>
  <style>
    body {
      margin: 0;
      padding: 24px;
      font-family: Arial, sans-serif;
      background: linear-gradient(180deg, #f6fbff 0%, #edf5ff 100%);
      color: #1f2937;
    }
    .card {
      max-width: 820px;
      margin: 0 auto;
      background: white;
      border-radius: 18px;
      padding: 28px;
      box-shadow: 0 10px 30px rgba(37, 99, 235, 0.08);
    }
    h1 {
      margin-top: 0;
      color: #1d4ed8;
    }
    .text {
      background: #f3f8ff;
      border-left: 5px solid #60a5fa;
      padding: 16px;
      border-radius: 10px;
      line-height: 1.6;
      margin: 18px 0;
    }
    label {
      display: block;
      font-weight: bold;
      margin-bottom: 8px;
    }
    textarea {
      width: 100%;
      min-height: 110px;
      padding: 12px;
      border: 2px solid #bfdbfe;
      border-radius: 10px;
      font-size: 16px;
      box-sizing: border-box;
      resize: vertical;
    }
    button {
      margin-top: 12px;
      background: #2563eb;
      color: white;
      border: none;
      border-radius: 10px;
      padding: 12px 18px;
      font-size: 16px;
      cursor: pointer;
    }
    .feedback {
      margin-top: 18px;
      padding: 12px 14px;
      border-radius: 10px;
      display: none;
      line-height: 1.5;
    }
    .success {
      background: #eafaf0;
      color: #166534;
    }
    .error {
      background: #fdecec;
      color: #991b1b;
    }
    .helper {
      margin-top: 16px;
      background: #eff6ff;
      border-radius: 10px;
      padding: 12px;
      display: none;
    }
  </style>
</head>
<body>
  <div class="card">
    <h1>Lee y responde</h1>
    <p>Lee el texto y responde con una frase breve.</p>

    <div class="text">
      El conejo se despertó temprano y vio que la hierba del jardín brillaba con gotas de rocío.
      Quiso correr, pero primero decidió escuchar el canto de los pájaros y observar cómo la luz del sol
      entraba por la ventana. Entonces comprendió que la mañana era un momento perfecto para empezar el día.
    </div>

    <label for="answer">¿Qué hizo el conejo antes de correr?</label>
    <textarea id="answer" placeholder="Escribe tu respuesta..."></textarea>
    <button id="checkBtn">Comprobar</button>
    <button id="helpBtn" style="margin-left: 8px; background: #0ea5e9;">Pedir ayuda con IA</button>

    <div id="feedback" class="feedback"></div>
    <div id="helper" class="helper"></div>
  </div>

  <script>
    const answerEl = document.getElementById('answer');
    const feedbackEl = document.getElementById('feedback');
    const helperEl = document.getElementById('helper');

    function showFeedback(text, kind) {
      feedbackEl.textContent = text;
      feedbackEl.className = 'feedback ' + kind;
      feedbackEl.style.display = 'block';
    }

    function sendToParent(type, payload = {}, requestId = String(Date.now())) {
      window.parent.postMessage({ type, requestId, payload }, '*');
    }

    document.getElementById('checkBtn').addEventListener('click', () => {
      const answer = answerEl.value.trim().toLowerCase();
      if (!answer) {
        showFeedback('Escribe una respuesta antes de comprobar.', 'error');
        return;
      }

      const ok = answer.includes('escuchar') || answer.includes('oyó') || answer.includes('escuchó') || answer.includes('escuchó el canto') || answer.includes('observó') || answer.includes('miró');
      if (ok) {
        showFeedback('¡Correcto! Antes de correr, el conejo escuchó y observó la mañana.', 'success');
      } else {
        showFeedback('Casi lo tienes. Antes de correr, el conejo escuchó el canto de los pájaros y observó la luz del sol.', 'error');
      }
    });

    document.getElementById('helpBtn').addEventListener('click', () => {
      helperEl.style.display = 'block';
      helperEl.textContent = 'Solicitando ayuda...';
      sendToParent('ia:request', {
        prompt: 'Explica esta pregunta con una pista muy sencilla para un niño de 8 años.',
        provider: 'openai',
        model: 'gpt-4o-mini'
      });
    });

    window.addEventListener('message', (event) => {
      const data = event.data;
      if (!data || typeof data !== 'object') return;

      if (data.type === 'exerciseserver:ia:result') {
        if (!data.ok) {
          helperEl.textContent = data.error || 'No se pudo obtener ayuda.';
          helperEl.style.display = 'block';
          return;
        }

        const text = data.data && data.data.text ? data.data.text : 'Sin respuesta.';
        helperEl.textContent = text;
        helperEl.style.display = 'block';
      }
    });

    sendToParent('ia:hello');
  </script>
</body>
</html>
```

Este patrón sirve para cualquier ejercicio:
- cambia el contenido visual
- cambia la lógica del reto
- mantén exactamente la parte de `postMessage` para pedir ayuda
- no añadas `fetch` ni peticiones HTTP directas al ejercicio

## 8. Prompt recomendado para que la IA genere un ejercicio

Usa este prompt base:

```text
Eres un especialista en diseño de ejercicios educativos para niños y adolescentes.
Genera un ejercicio interactivo en formato HTML puro, sin frameworks, sin bibliotecas externas y sin llamadas HTTP ni fetch.

Debe ejecutarse dentro de un iframe sandbox con JavaScript habilitado.
Debe ser completamente autocontenido y no usar APIs externas.
Debe poder pedir ayuda a la IA mediante postMessage a la ventana padre, nunca con fetch directo.

Objetivo:
- crear un ejercicio didáctico útil, claro y atractivo
- mantener la interfaz sencilla y visualmente escolar
- incluir instrucciones, contenido visual, interacción y feedback
- usar solo HTML + CSS + JavaScript vanilla

Requisitos:
- Título claro.
- Instrucciones breves.
- Una sola actividad principal.
- Interacción sencilla con botones, inputs o selección.
- Feedback visible en pantalla.
- Diseño responsive.
- Sin comentarios del desarrollador en el HTML final.
- Sin texto largo ni excesivamente complejo.

Incluye estas variables:
- Tema: {TEMA}
- Nivel: {NIVEL}
- Edad: {EDAD}
- Tipo de ejercicio: {TIPO}
- Objetivo pedagógico: {OBJETIVO}
- Duración estimada: {DURACION}
- Estilo visual: {ESTILO}
- Necesidad de ayuda de IA: {SI/NO}

Si la ayuda de IA es necesaria, incluye un botón o acción que haga:
window.parent.postMessage({ type: 'ia:request', payload: { prompt: '...' } }, '*');

y una escucha en window.addEventListener('message', ...) para manejar la respuesta.

Devuelve SOLO el código HTML final, sin explicaciones, sin markdown y sin bloques de código delimitados.
```

## 9. Plantilla de relleno para cualquier ejercicio

```text
Tema: {TEMA}
Curso/nivel: {NIVEL}
Edad aproximada: {EDAD}
Tipo de ejercicio: {TIPO}
Objetivo pedagógico: {OBJETIVO}
Duración estimada: {DURACION}
Estilo visual: {ESTILO}
Necesidad de ayuda de IA: {SI/NO}

Genera el ejercicio en HTML puro y seguro, compatible con sandbox y con soporte de IA mediante postMessage.
```

## 10. Resultado esperado

El ejercicio generado debe ser un único archivo HTML listo para subir al catálogo y que cumpla estas condiciones:

- no usa `fetch`
- no usa `XMLHttpRequest`
- no usa APIs externas
- no accede a cookies ni sesión
- usa `postMessage` para pedir ayuda de IA
- funciona dentro del visor del proyecto
- es útil para niños y tiene instrucción clara

## 11. Recomendación práctica

Para cualquier ejercicio que quiera usar IA:

1. Genera la actividad base en HTML puro.
2. Añade una acción de ayuda opcional.
3. Usa `ia:hello` para comprobar que la IA está disponible.
4. Usa `ia:request` para pedir ayuda contextual.
5. Maneja la respuesta con `window.addEventListener('message')`.
6. No fuerces peticiones directas desde el iframe.

## 12. Resumen corto

La regla definitiva es esta:

> El iframe no llama a la API. El iframe pregunta a la ventana padre. La ventana padre usa la sesión autenticada del usuario para hacer la petición al backend. El backend responde a la ventana padre y la ventana padre reenvía la respuesta al iframe.

Esto es lo que hace que el sistema sea seguro, compatible con sandbox y usable para cualquier tipo de ejercicio.
