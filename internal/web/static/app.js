"use strict";
(function () {
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => Array.from(el.querySelectorAll(s));

  // Confirmation for destructive forms (inline handlers are blocked by the CSP).
  $$("form[data-confirm]").forEach((f) =>
    f.addEventListener("submit", (e) => {
      if (!confirm(f.dataset.confirm)) e.preventDefault();
    })
  );

  // Copy buttons: <button data-copy="input-id">.
  $$("button[data-copy]").forEach((b) =>
    b.addEventListener("click", async () => {
      const input = document.getElementById(b.dataset.copy);
      if (!input) return;
      input.select();
      try {
        await navigator.clipboard.writeText(input.value);
      } catch (_) {
        document.execCommand("copy");
      }
      const old = b.textContent;
      b.textContent = "Copiado";
      setTimeout(() => (b.textContent = old), 1500);
    })
  );

  // ---------- catalogue filters ----------
  const cards = $("#cards");
  if (cards && $("#filters")) {
    const state = { q: "", levels: new Set(), kinds: new Set(), tags: new Set() };
    const q = $("#q");
    const all = $$(".card", cards);
    const norm = (s) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "");
    all.forEach((c) => {
      c._text = norm(c.dataset.text || "");
      c._tags = c.dataset.tags ? c.dataset.tags.split("|") : [];
    });

    const fromURL = () => {
      const p = new URLSearchParams(location.search);
      state.q = p.get("q") || "";
      state.levels = new Set(p.getAll("nivel"));
      state.kinds = new Set(p.getAll("tipo"));
      state.tags = new Set(p.getAll("etiqueta"));
      q.value = state.q;
    };
    const toURL = () => {
      const p = new URLSearchParams();
      if (state.q) p.set("q", state.q);
      state.levels.forEach((v) => p.append("nivel", v));
      state.kinds.forEach((v) => p.append("tipo", v));
      state.tags.forEach((v) => p.append("etiqueta", v));
      const s = p.toString();
      history.replaceState(null, "", s ? "?" + s : location.pathname);
    };

    const apply = () => {
      const words = norm(state.q).split(/\s+/).filter(Boolean);
      let shown = 0;
      all.forEach((c) => {
        const ok =
          (state.levels.size === 0 || state.levels.has(c.dataset.level)) &&
          (state.kinds.size === 0 || state.kinds.has(c.dataset.kind)) &&
          [...state.tags].every((t) => c._tags.includes(t)) &&
          words.every((w) => c._text.includes(w) || c._tags.some((t) => norm(t).includes(w)));
        c.hidden = !ok;
        if (ok) shown++;
      });
      $$(".level-chip").forEach((b) => b.classList.toggle("on", state.levels.has(b.dataset.level)));
      $$(".kind-chip").forEach((b) => b.classList.toggle("on", state.kinds.has(b.dataset.kind)));
      $$(".tag-chip").forEach((b) => b.classList.toggle("on", state.tags.has(b.dataset.tag)));
      const filtering = state.q || state.levels.size || state.kinds.size || state.tags.size;
      $("#count").textContent = filtering ? `${shown} de ${all.length} ejercicios` : `${all.length} ejercicios`;
      $("#clear").hidden = !filtering;
      $("#no-results").hidden = !(all.length && shown === 0);
      toURL();
    };

    const toggle = (set, v) => (set.has(v) ? set.delete(v) : set.add(v));
    $$(".level-chip").forEach((b) => b.addEventListener("click", () => { toggle(state.levels, b.dataset.level); apply(); }));
    $$(".kind-chip").forEach((b) => b.addEventListener("click", () => { toggle(state.kinds, b.dataset.kind); apply(); }));
    $$(".tag-chip").forEach((b) => b.addEventListener("click", () => { toggle(state.tags, b.dataset.tag); apply(); }));
    q.addEventListener("input", () => { state.q = q.value.trim(); apply(); });
    $("#clear").addEventListener("click", () => {
      state.q = ""; q.value = "";
      state.levels.clear(); state.kinds.clear(); state.tags.clear();
      apply();
    });
    fromURL();
    apply();
  }

  // ---------- IA model loader ----------
  // Same flow on /mi-ia and /admin/usuarios: provider + key, "Cargar modelos", pick, save.
  document.querySelectorAll("[data-ai-form]").forEach((form) => {
    const aiProvider = form.querySelector('select[name="provider"]');
    const aiApiKey = form.querySelector('input[name="api_key"]');
    const aiModel = form.querySelector('select[name="model"]');
    const aiLoadButton = form.querySelector("[data-load-models]");
    const aiCsrfInput = form.querySelector('input[name="csrf"]');
    if (!aiProvider || !aiApiKey || !aiModel || !aiCsrfInput) return;
    const setOptions = (models, selected) => {
      const list = Array.isArray(models) ? [...models] : [];
      if (selected && !list.includes(selected)) list.unshift(selected);
      aiModel.innerHTML = "";
      if (list.length === 0) {
        const empty = document.createElement("option");
        empty.value = "";
        empty.textContent = "Sin modelo";
        aiModel.appendChild(empty);
        return;
      }
      list.forEach((model) => {
        const option = document.createElement("option");
        option.value = model;
        option.textContent = model;
        if (model === selected) option.selected = true;
        aiModel.appendChild(option);
      });
    };
    const canLoad = () => !aiApiKey.disabled && aiProvider.value && aiProvider.value !== "none" && aiApiKey.value.trim();

    const loadModels = () => {
      const selectedProvider = aiProvider.value;
      const key = (aiApiKey.value || "").trim();
      if (!selectedProvider || selectedProvider === "none" || !key) {
        setOptions([], "");
        return;
      }
      const params = { csrf: aiCsrfInput.value, provider: selectedProvider, api_key: key };
      if (form.dataset.userId) params.user_id = form.dataset.userId;
      fetch("/api/ia/models", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8" },
        body: new URLSearchParams(params).toString(),
      })
        .then((r) => r.json().then((json) => ({ status: r.status, body: json })))
        .then(({ status, body }) => {
          if (status >= 400 || body.ok === false) {
            throw new Error(body && body.error ? body.error : "No se pudieron cargar los modelos");
          }
          const selected = aiModel.value || body.defaultModel || "";
          setOptions(Array.isArray(body.models) ? body.models : [], selected);
        })
        .catch((err) => {
          const message = err && err.message ? err.message : String(err);
          setOptions([], "");
          window.alert(message);
        });
    };

    aiProvider.addEventListener("change", loadModels);
    aiApiKey.addEventListener("input", () => { if (canLoad()) loadModels(); });
    if (aiLoadButton) aiLoadButton.addEventListener("click", loadModels);
    if ("aiAutoload" in form.dataset && canLoad()) loadModels();
  });

  // ---------- token visibility ----------
  document.querySelectorAll("[data-toggle-token]").forEach((button) => {
    const targetId = button.dataset.toggleToken;
    const input = targetId ? document.getElementById(targetId) : button.closest(".token-field")?.querySelector("input");
    if (!input) return;
    button.addEventListener("click", () => {
      const show = input.type === "password";
      input.type = show ? "text" : "password";
      button.textContent = show ? "Ocultar" : "Ver token";
    });
  });

  // ---------- viewer ----------
  const back = $("[data-back]");
  if (back && document.referrer) {
    try {
      const ref = new URL(document.referrer);
      if (ref.origin === location.origin && ref.pathname === "/") back.href = ref.pathname + ref.search;
    } catch (_) {}
  }
  const fs = $("#fullscreen");
  const frame = $("#exercise");
  if (fs && frame) {
    if (!frame.requestFullscreen) fs.hidden = true;
    fs.addEventListener("click", () => frame.requestFullscreen && frame.requestFullscreen());

    const respondToFrame = (source, payload) => {
      if (!source || typeof source.postMessage !== "function") return;
      source.postMessage({ ...payload, source: "exerciseserver" }, "*");
    };

    const readJSONResponse = async (response, label) => {
      const raw = await response.text();
      const preview = raw.trim().slice(0, 300);
      console.debug(`[exercise-bridge] ${label} raw response`, {
        status: response.status,
        contentType: response.headers.get("content-type"),
        preview,
      });

      if (!raw || !raw.trim()) {
        return { status: response.status, body: { ok: false, code: "empty_response", error: "La respuesta estaba vacía." } };
      }

      if (preview.startsWith("<")) {
        throw new Error(`La API devolvió HTML en lugar de JSON (${response.status}). Respuesta: ${preview}`);
      }

      try {
        return { status: response.status, body: JSON.parse(raw) };
      } catch (err) {
        throw new Error(`La API devolvió un cuerpo no JSON (${response.status}). Respuesta: ${preview}`);
      }
    };

    const handleExerciseMessage = (event) => {
      if (!event || !event.data || typeof event.data !== "object") return;
      if (event.source !== frame.contentWindow) return;
      const type = event.data.type;
      const requestId = event.data.requestId || null;
      const supported = new Set(["exerciseserver:ia", "exerciseserver:ia:config", "ia:hello", "ia:config", "ia:request"]);
      if (!supported.has(type)) {
        console.warn("[exercise-bridge] Ignored unsupported message", { type, requestId, data: event.data });
        return;
      }

      console.debug("[exercise-bridge] Message received", { type, requestId, origin: event.origin, payload: event.data.payload || null });

      const sendFailure = (code, message, extra = {}) => {
        const responseType = type === "exerciseserver:ia" || type === "ia:request" ? "exerciseserver:ia:result" : (type === "ia:hello" || type === "ia:config" || type === "exerciseserver:ia:config" ? "ia:config" : "ia:result");
        console.warn("[exercise-bridge] Sending failure response", { type, requestId, code, message, extra });
        respondToFrame(event.source, {
          type: responseType,
          requestId,
          ok: false,
          code,
          error: message,
          ...extra,
        });
      };

      const sendSuccess = (payload, responseTypeOverride) => {
        const responseType = responseTypeOverride || (type === "exerciseserver:ia" || type === "ia:request" ? "exerciseserver:ia:result" : "ia:config");
        console.debug("[exercise-bridge] Sending success response", { type, requestId, responseType, payload });
        respondToFrame(event.source, {
          type: responseType,
          requestId,
          ok: true,
          ...payload,
        });
      };

      const emitConfigResponse = (cfg) => {
        const ids = Array.isArray(cfg && cfg.providers) ? cfg.providers.map((p) => typeof p === "string" ? p : p.id) : [];
        const replyPayload = {
          version: 1,
          ok: cfg && cfg.ok !== false,
          status: cfg && cfg.status ? cfg.status : 200,
          code: cfg && cfg.code ? cfg.code : undefined,
          providers: ids,
          defaultProvider: cfg && cfg.defaultProvider ? cfg.defaultProvider : (ids[0] || "none"),
          images: !!(cfg && cfg.images === true),
          maxImages: Math.min(cfg && cfg.maxImages ? cfg.maxImages : 3, 3),
        };
        if (cfg && cfg.error) replyPayload.error = cfg.error;
        if (cfg && cfg.loginUrl) replyPayload.loginUrl = cfg.loginUrl;
        sendSuccess(replyPayload, "ia:config");
      };

      if (type === "ia:hello" || type === "ia:config" || type === "exerciseserver:ia:config") {
        console.debug("[exercise-bridge] Fetching IA config", { type, requestId });
        fetch("/api/ia/config", { credentials: "same-origin" })
          .then((r) => readJSONResponse(r, "IA config"))
          .then(({ status, body }) => {
            console.debug("[exercise-bridge] IA config response", { type, requestId, status, body });
            if (status >= 400) {
              const cfg = body && typeof body === "object" ? body : { ok: false, code: "request_failed", error: "No se pudo cargar la configuración de IA" };
              emitConfigResponse({ ok: false, status, code: cfg.code || "request_failed", error: cfg.error || "No se pudo cargar la configuración de IA", loginUrl: cfg.loginUrl || "/login" });
              return;
            }
            emitConfigResponse({ ok: true, status, ...body });
          })
          .catch((err) => {
            const message = String(err && err.message ? err.message : err);
            console.error("[exercise-bridge] Error fetching IA config", { type, requestId, error: message });
            emitConfigResponse({ ok: false, status: 500, code: "request_failed", error: message, loginUrl: "/login" });
          });
        return;
      }

      const payload = event.data.payload || {};
      console.debug("[exercise-bridge] Sending IA request", { type, requestId, payload });
      fetch("/api/ia", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      })
        .then((r) => readJSONResponse(r, "IA request"))
        .then(({ status, body }) => {
          console.debug("[exercise-bridge] IA request response", { type, requestId, status, body });
          if (status >= 400) {
            sendFailure(body && body.code ? body.code : "request_failed", body && body.error ? body.error : "No se pudo consultar la IA");
            return;
          }
          sendSuccess({ data: body });
        })
        .catch((err) => {
          const message = String(err && err.message ? err.message : err);
          console.error("[exercise-bridge] Error sending IA request", { type, requestId, payload, error: message });
          sendFailure("request_failed", message);
        });
    };

    window.addEventListener("message", handleExerciseMessage);
  }

  // ---------- admin: tag suggestions ----------
  $$(".add-tag").forEach((b) =>
    b.addEventListener("click", () => {
      const input = b.closest("form").querySelector(".tags-input");
      const cur = input.value.split(",").map((s) => s.trim()).filter(Boolean);
      if (!cur.some((t) => t.toLowerCase() === b.dataset.tag.toLowerCase())) cur.push(b.dataset.tag);
      input.value = cur.join(", ");
      input.focus();
    })
  );

  // ---------- admin: upload dropzone ----------
  const dz = $("#dropzone");
  if (dz) {
    const input = $("input[type=file]", dz);
    const list = $("#file-list");
    const show = () => {
      list.innerHTML = "";
      Array.from(input.files).forEach((f) => {
        const li = document.createElement("li");
        li.textContent = f.name;
        list.appendChild(li);
      });
    };
    input.addEventListener("change", show);
    ["dragenter", "dragover"].forEach((ev) => dz.addEventListener(ev, (e) => { e.preventDefault(); dz.classList.add("over"); }));
    ["dragleave", "drop"].forEach((ev) => dz.addEventListener(ev, () => dz.classList.remove("over")));
    dz.addEventListener("drop", (e) => {
      e.preventDefault();
      if (e.dataTransfer && e.dataTransfer.files.length) {
        input.files = e.dataTransfer.files;
        show();
      }
    });
    const form = $("#upload-form");
    form.addEventListener("submit", () => {
      const btn = $("button[type=submit]", form);
      btn.disabled = true;
      btn.textContent = "Subiendo…";
    });
  }

  // ---------- admin: table filter ----------
  $$(".table-filter").forEach((inp) => {
    const rows = $$("tbody tr", document.getElementById(inp.dataset.table));
    inp.addEventListener("input", () => {
      const v = inp.value.toLowerCase();
      rows.forEach((r) => (r.hidden = v && !r.textContent.toLowerCase().includes(v)));
    });
  });
})();
