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
