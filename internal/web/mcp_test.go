package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var rawTokenRe = regexp.MustCompile(`id="tok-raw" value="([^"]+)"|value="(exs_[0-9a-f]+)" id="tok-raw"`)

// createTokenViaAdmin creates a token through the admin page and returns the raw value shown once.
func createTokenViaAdmin(t *testing.T, c *http.Client, base, csrf, name, duration string) string {
	t.Helper()
	resp, err := c.PostForm(base+"/admin/tokens", url.Values{"csrf": {csrf}, "name": {name}, "duration": {duration}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("crear token: %d %s", resp.StatusCode, body)
	}
	m := rawTokenRe.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("no se muestra el token: %s", body)
	}
	tok := m[1] + m[2]
	if !strings.Contains(string(body), base+"/mcp/"+tok) {
		t.Fatal("falta la URL para claude.ai")
	}
	return tok
}

type mcpReply struct {
	Status int
	Body   map[string]any
}

func mcpCall(t *testing.T, endpoint, bearer string, msg any) mcpReply {
	t.Helper()
	b, _ := json.Marshal(msg)
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := mcpReply{Status: resp.StatusCode}
	data, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &out.Body); err != nil {
			t.Fatalf("respuesta no JSON (%d): %s", resp.StatusCode, data)
		}
	}
	return out
}

func toolCall(id int, name string, args map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}}
}

func resultText(t *testing.T, r mcpReply) (string, bool) {
	t.Helper()
	res, ok := r.Body["result"].(map[string]any)
	if !ok {
		t.Fatalf("sin result: %v", r.Body)
	}
	content := res["content"].([]any)
	isErr, _ := res["isError"].(bool)
	return content[0].(map[string]any)["text"].(string), isErr
}

func TestMCPTokensAndUpload(t *testing.T) {
	ts, st := newTestServer(t)
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateLevel("2º Primaria", 20); err != nil {
		t.Fatal(err)
	}
	c := client(t)
	login(t, c, ts.URL, "admin", "secret-admin")
	csrf := csrfToken(t, c, ts.URL)
	tok := createTokenViaAdmin(t, c, ts.URL, csrf, "Claude web", "1h")

	// Without a token, or with a wrong one: 401.
	if r := mcpCall(t, ts.URL+"/mcp", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}); r.Status != 401 {
		t.Fatalf("sin token: %d", r.Status)
	}
	if r := mcpCall(t, ts.URL+"/mcp/exs_nope", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}); r.Status != 401 {
		t.Fatalf("token falso: %d", r.Status)
	}

	// initialize via Bearer header.
	r := mcpCall(t, ts.URL+"/mcp", tok, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}})
	if r.Status != 200 {
		t.Fatalf("initialize: %d %v", r.Status, r.Body)
	}
	if v := r.Body["result"].(map[string]any)["protocolVersion"]; v != "2025-06-18" {
		t.Fatalf("protocolVersion: %v", v)
	}
	// Notifications get 202 with no body.
	if r := mcpCall(t, ts.URL+"/mcp", tok, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); r.Status != 202 {
		t.Fatalf("notification: %d", r.Status)
	}
	// GET is not supported (no SSE stream).
	if resp, _ := http.Get(ts.URL + "/mcp/" + tok); resp.StatusCode != 405 {
		t.Fatalf("GET: %d", resp.StatusCode)
	}

	// tools/list via token in the path (claude.ai style).
	pathURL := ts.URL + "/mcp/" + tok
	r = mcpCall(t, pathURL, "", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	tools := r.Body["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 7 {
		t.Fatalf("tools: %v", tools)
	}

	if txt, _ := resultText(t, mcpCall(t, pathURL, "", toolCall(3, "listar_niveles", nil))); !strings.Contains(txt, "2º Primaria") {
		t.Fatalf("niveles: %s", txt)
	}

	page := `<!doctype html><html><head><title>Restas</title></head><body><p>5-3=?</p></body></html>`
	args := map[string]any{"nombre_fichero": "restas", "html": page, "titulo": "Restas sencillas",
		"descripcion": "Restas sin llevar", "nivel": "2º primaria", "etiquetas": []string{"matemáticas", "restas"}}

	txt, isErr := resultText(t, mcpCall(t, pathURL, "", toolCall(4, "subir_ejercicio", args)))
	if isErr || !strings.Contains(txt, "/ver/") {
		t.Fatalf("subir: %s", txt)
	}
	files, _ := st.ListFiles()
	if len(files) != 1 {
		t.Fatalf("ficheros: %d", len(files))
	}
	f := files[0]
	if f.Title != "Restas sencillas" || f.OrigName != "restas.html" || f.LevelName != "2º Primaria" ||
		f.Description != "Restas sin llevar" || strings.Join(f.Tags, ",") != "matemáticas,restas" {
		t.Fatalf("fichero mal guardado: %+v", f)
	}

	// Same content again: not duplicated.
	txt, isErr = resultText(t, mcpCall(t, pathURL, "", toolCall(5, "subir_ejercicio", args)))
	if isErr || !strings.Contains(txt, "no se ha duplicado") {
		t.Fatalf("duplicado: %s", txt)
	}
	if files, _ := st.ListFiles(); len(files) != 1 {
		t.Fatalf("se ha duplicado: %d", len(files))
	}

	// Unknown level is a tool error that lists the valid ones, and nothing is created.
	args["nivel"], args["nombre_fichero"] = "9º Galáctico", "otro.html"
	txt, isErr = resultText(t, mcpCall(t, pathURL, "", toolCall(6, "subir_ejercicio", args)))
	if !isErr || !strings.Contains(txt, "2º Primaria") {
		t.Fatalf("nivel inexistente: %v %s", isErr, txt)
	}
	// Not HTML.
	txt, isErr = resultText(t, mcpCall(t, pathURL, "", toolCall(7, "subir_ejercicio", map[string]any{"nombre_fichero": "x.html", "html": "hola"})))
	if !isErr {
		t.Fatalf("html inválido aceptado: %s", txt)
	}
	if files, _ := st.ListFiles(); len(files) != 1 {
		t.Fatalf("ficheros tras errores: %d", len(files))
	}

	// An API token does not grant access to the web interface.
	req, _ := http.NewRequest("GET", ts.URL+"/admin/ficheros", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := client(t).Do(req)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("el token da acceso a admin: %d", resp.StatusCode)
	}

	// The list shows last use; revoke then delete.
	list, _ := st.ListAPITokens()
	if len(list) != 1 || list[0].LastUsedAt.IsZero() || list[0].Status(time.Now()) != "activo" {
		t.Fatalf("lista: %+v", list)
	}
	id := list[0].ID
	resp, _ = c.PostForm(ts.URL+"/admin/tokens/"+itoa(id)+"/revocar", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revocar: %d", resp.StatusCode)
	}
	if r := mcpCall(t, pathURL, "", map[string]any{"jsonrpc": "2.0", "id": 8, "method": "ping"}); r.Status != 401 {
		t.Fatalf("token revocado sigue funcionando: %d", r.Status)
	}
	if list, _ := st.ListAPITokens(); list[0].Status(time.Now()) != "revocado" {
		t.Fatalf("estado: %s", list[0].Status(time.Now()))
	}

	// Delete an active token directly: it stops working and disappears.
	tok2 := createTokenViaAdmin(t, c, ts.URL, csrf, "Claude Code", "never")
	if r := mcpCall(t, ts.URL+"/mcp", tok2, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "ping"}); r.Status != 200 {
		t.Fatalf("tok2: %d", r.Status)
	}
	list, _ = st.ListAPITokens()
	var id2 int64
	for _, x := range list {
		if x.Name == "Claude Code" {
			id2 = x.ID
			if !x.ExpiresAt.IsZero() {
				t.Fatal("«Sin caducidad» tiene caducidad")
			}
		}
	}
	resp, _ = c.PostForm(ts.URL+"/admin/tokens/"+itoa(id2)+"/borrar", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("borrar: %d", resp.StatusCode)
	}
	if r := mcpCall(t, ts.URL+"/mcp", tok2, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "ping"}); r.Status != 401 {
		t.Fatalf("token borrado sigue funcionando: %d", r.Status)
	}
	if list, _ := st.ListAPITokens(); len(list) != 1 {
		t.Fatalf("el token borrado sigue en la lista: %d", len(list))
	}

	// Admin page renders the list.
	resp, _ = c.Get(ts.URL + "/admin/tokens")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Claude web") || strings.Contains(string(body), tok) {
		t.Fatalf("página de tokens: %d", resp.StatusCode)
	}

	// Non-admins cannot create tokens.
	st.CreateUser("nino", "1234", false)
	cn := client(t)
	login(t, cn, ts.URL, "nino", "1234")
	resp, _ = cn.PostForm(ts.URL+"/admin/tokens", url.Values{"csrf": {csrfToken(t, cn, ts.URL)}, "name": {"x"}, "duration": {"1h"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no admin crea tokens: %d", resp.StatusCode)
	}
}

func TestAPITokenExpiry(t *testing.T) {
	_, st := newTestServer(t)
	raw, _, err := st.CreateAPIToken("corto", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.ValidateAPIToken(raw); !ok {
		t.Fatal("token recién creado no válido")
	}
	time.Sleep(2100 * time.Millisecond)
	if _, ok := st.ValidateAPIToken(raw); ok {
		t.Fatal("token caducado sigue siendo válido")
	}
	list, _ := st.ListAPITokens()
	if list[0].Status(time.Now()) != "caducado" {
		t.Fatalf("estado: %s", list[0].Status(time.Now()))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestMCPListReadUpdate(t *testing.T) {
	ts, st := newTestServer(t)
	st.CreateLevel("1º Primaria", 10)
	st.CreateLevel("2º Primaria", 20)
	raw, _, err := st.CreateAPIToken("t", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ep := ts.URL + "/mcp/" + raw
	call := func(id int, name string, args map[string]any) (string, bool, map[string]any) {
		r := mcpCall(t, ep, "", toolCall(id, name, args))
		txt, isErr := resultText(t, r)
		sc, _ := r.Body["result"].(map[string]any)["structuredContent"].(map[string]any)
		return txt, isErr, sc
	}

	v1 := `<!doctype html><html><head><title>Sumas</title></head><body>2+2</body></html>`
	if txt, isErr, _ := call(1, "subir_ejercicio", map[string]any{"nombre_fichero": "sumas.html", "html": v1,
		"nivel": "1º Primaria", "etiquetas": []string{"matemáticas", "sumas"}, "descripcion": "Sumas fáciles"}); isErr {
		t.Fatal(txt)
	}
	call(2, "subir_ejercicio", map[string]any{"nombre_fichero": "letras.html",
		"html": `<html><body>abc</body></html>`, "etiquetas": []string{"lengua"}})

	// List with filters (accent-insensitive).
	_, _, sc := call(3, "listar_ejercicios", nil)
	if sc["total"].(float64) != 2 {
		t.Fatalf("total: %v", sc)
	}
	txt, _, sc := call(4, "listar_ejercicios", map[string]any{"etiqueta": "Matematicas"})
	if sc["total"].(float64) != 1 || !strings.Contains(txt, "Sumas") {
		t.Fatalf("filtro etiqueta: %s", txt)
	}
	if _, _, sc := call(5, "listar_ejercicios", map[string]any{"texto": "FACILES", "nivel": "1º primaria"}); sc["total"].(float64) != 1 {
		t.Fatalf("filtro texto+nivel: %v", sc)
	}
	ex := sc["ejercicios"].([]any)[0].(map[string]any)
	id := int(ex["id"].(float64))

	// Read returns metadata and the full HTML.
	txt, isErr, sc := call(6, "leer_ejercicio", map[string]any{"id": id})
	if isErr || sc["html"] != v1 || !strings.Contains(txt, "Sumas fáciles") || !strings.Contains(txt, "1º Primaria") {
		t.Fatalf("leer: %s", txt)
	}
	if _, isErr, _ := call(7, "leer_ejercicio", map[string]any{"id": 9999}); !isErr {
		t.Fatal("leer id inexistente sin error")
	}

	// Update only the content: id, title, level and tags are kept.
	v2 := `<!doctype html><html><head><title>Otro título</title></head><body>2+2=4, 3+3=?</body></html>`
	if txt, isErr, _ := call(8, "actualizar_ejercicio", map[string]any{"id": id, "html": v2}); isErr {
		t.Fatal(txt)
	}
	f, _ := st.GetFile(int64(id))
	if f.Title != "Sumas" || f.LevelName != "1º Primaria" || len(f.Tags) != 2 || f.Size != int64(len(v2)) {
		t.Fatalf("tras actualizar contenido: %+v", f)
	}
	if _, _, sc := call(9, "leer_ejercicio", map[string]any{"id": id}); sc["html"] != v2 {
		t.Fatal("el contenido no ha cambiado")
	}

	// Update metadata only; empty level clears it, [] clears tags.
	if txt, isErr, _ := call(10, "actualizar_ejercicio", map[string]any{"id": id, "titulo": "Sumas y más", "nivel": "", "etiquetas": []string{}}); isErr {
		t.Fatal(txt)
	}
	f, _ = st.GetFile(int64(id))
	if f.Title != "Sumas y más" || f.LevelName != "" || len(f.Tags) != 0 || f.Description != "Sumas fáciles" {
		t.Fatalf("tras actualizar metadatos: %+v", f)
	}

	// Invalid update changes nothing.
	if _, isErr, _ := call(11, "actualizar_ejercicio", map[string]any{"id": id, "html": "<html><body>x</body></html>", "nivel": "No existe"}); !isErr {
		t.Fatal("nivel inexistente aceptado")
	}
	if g, _ := st.GetFile(int64(id)); g.Size != int64(len(v2)) {
		t.Fatal("actualización parcial aplicada pese al error")
	}
	if _, isErr, _ := call(12, "actualizar_ejercicio", map[string]any{"id": id}); !isErr {
		t.Fatal("actualizar sin cambios aceptado")
	}
	if files, _ := st.ListFiles(); len(files) != 2 {
		t.Fatalf("ficheros: %d", len(files))
	}
}

func TestInstructionsAvailable(t *testing.T) {
	ts, st := newTestServer(t)
	st.SetAdminPassword("secret-admin")
	raw, _, _ := st.CreateAPIToken("t", time.Hour)

	// Via MCP.
	txt, isErr := resultText(t, mcpCall(t, ts.URL+"/mcp", raw, toolCall(1, "obtener_instrucciones", nil)))
	if isErr || !strings.Contains(txt, "# Instrucciones para crear y publicar ejercicios") || !strings.Contains(txt, "estrategia del catálogo") {
		t.Fatalf("instrucciones por MCP: %.200s", txt)
	}
	init := mcpCall(t, ts.URL+"/mcp", raw, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "initialize", "params": map[string]any{}})
	if !strings.Contains(init.Body["result"].(map[string]any)["instructions"].(string), "obtener_instrucciones") {
		t.Fatal("initialize no menciona obtener_instrucciones")
	}

	// Via web: requires login, then view and download.
	anon := client(t)
	if resp, _ := anon.Get(ts.URL + "/instrucciones-ia.md"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sin sesión: %d", resp.StatusCode)
	}
	c := client(t)
	login(t, c, ts.URL, "admin", "secret-admin")
	resp, _ := c.Get(ts.URL + "/instrucciones-ia.md?descargar=1")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "INSTRUCCIONES_CLAUDE.md") || string(body) != txt {
		t.Fatalf("descarga: %d %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	resp, _ = c.Get(ts.URL + "/admin/tokens")
	page, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(page), `href="/instrucciones-ia.md?descargar=1"`) || !strings.Contains(string(page), "obtener_instrucciones") {
		t.Fatal("la página de tokens no enlaza las instrucciones")
	}
}
