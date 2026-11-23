package web

// Minimal MCP server (Streamable HTTP transport, stateless, JSON responses only).
// It is authenticated with API tokens created in /admin/tokens, sent either as
// "Authorization: Bearer <token>" to /mcp or embedded in the path as /mcp/<token>
// (for clients such as claude.ai that cannot send custom headers).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/redjhawk/exerciseserver"
	"github.com/redjhawk/exerciseserver/internal/store"
)

const (
	mcpMaxBody      = 20 << 20
	mcpMaxHTML      = 10 << 20
	mcpLatestProto  = "2025-06-18"
	mcpServerName   = "exerciseserver"
	mcpInstructions = `Servidor de ejercicios educativos para niños.
ANTES de crear o modificar un ejercicio, llama a obtener_instrucciones y síguelas al pie de la letra: explican cómo debe ser el HTML, cómo usar la IA de la plataforma (y cómo funcionar sin ella) y cómo publicar.
Usa listar_niveles y listar_etiquetas para elegir metadatos que ya existen y subir_ejercicio para publicar un ejercicio HTML nuevo. Para consultar o modificar uno existente usa listar_ejercicios, leer_ejercicio y actualizar_ejercicio (con el mismo id; no subas una copia nueva).`
)

var mcpSupportedProtos = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) mcpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Stateless server: no SSE stream (GET) and no sessions to terminate (DELETE).
		w.Header().Set("Allow", "POST")
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	tok, ok := s.store.ValidateAPIToken(apiTokenFromRequest(r))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="exerciseserver"`)
		writeJSON(w, http.StatusUnauthorized, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &mcpError{Code: -32001, Message: "token no válido, caducado o revocado"}})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpMaxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &mcpError{Code: -32600, Message: "petición demasiado grande"}})
		return
	}
	body = bytes.TrimSpace(body)

	// Batches were allowed in protocol 2025-03-26; support them for older clients.
	if len(body) > 0 && body[0] == '[' {
		var reqs []mcpRequest
		if err := json.Unmarshal(body, &reqs); err != nil || len(reqs) == 0 {
			writeJSON(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &mcpError{Code: -32700, Message: "JSON no válido"}})
			return
		}
		var out []mcpResponse
		for _, req := range reqs {
			if resp, ok := s.mcpDispatch(r, tok, req); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	var req mcpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &mcpError{Code: -32700, Message: "JSON no válido"}})
		return
	}
	resp, ok := s.mcpDispatch(r, tok, req)
	if !ok {
		w.WriteHeader(http.StatusAccepted) // notification or response from the client
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// mcpDispatch handles one JSON-RPC message. ok=false means there is nothing to answer (notification).
func (s *Server) mcpDispatch(r *http.Request, tok store.APIToken, req mcpRequest) (mcpResponse, bool) {
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return mcpResponse{}, false
	}
	resp := mcpResponse{JSONRPC: "2.0", ID: req.ID}
	if req.JSONRPC != "2.0" || req.Method == "" {
		resp.Error = &mcpError{Code: -32600, Message: "petición JSON-RPC no válida"}
		return resp, true
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		proto := mcpLatestProto
		if contains(mcpSupportedProtos, p.ProtocolVersion) {
			proto = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": proto,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": mcpServerName, "title": "Ejercicios", "version": s.version},
			"instructions":    mcpInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &mcpError{Code: -32602, Message: "parámetros no válidos"}
			return resp, true
		}
		text, structured, err := s.mcpCallTool(r, tok, p.Name, p.Arguments)
		if errors.Is(err, errUnknownTool) {
			resp.Error = &mcpError{Code: -32602, Message: "herramienta desconocida: " + p.Name}
			return resp, true
		}
		if err != nil {
			resp.Result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
			return resp, true
		}
		res := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
		if structured != nil {
			res["structuredContent"] = structured
		}
		resp.Result = res
	default:
		resp.Error = &mcpError{Code: -32601, Message: "método no soportado: " + req.Method}
	}
	return resp, true
}

var errUnknownTool = errors.New("unknown tool")

func mcpTools() []any {
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	readOnly := map[string]any{"readOnlyHint": true, "openWorldHint": false}
	return []any{
		map[string]any{
			"name": "obtener_instrucciones", "title": "Obtener instrucciones",
			"description": "Devuelve las instrucciones actualizadas (Markdown) para crear, publicar y modificar ejercicios: formato del HTML, uso de la IA de la plataforma y modo sin IA, código base y flujo de publicación. Llámala antes de crear o modificar un ejercicio.",
			"inputSchema": obj(map[string]any{}), "annotations": readOnly,
		},
		map[string]any{
			"name": "listar_niveles", "title": "Listar niveles",
			"description": "Devuelve los niveles educativos que existen (p. ej. «1º Primaria»), en orden. Úsalo antes de subir un ejercicio para elegir el nivel exacto.",
			"inputSchema": obj(map[string]any{}), "annotations": readOnly,
		},
		map[string]any{
			"name": "listar_etiquetas", "title": "Listar etiquetas",
			"description": "Devuelve las etiquetas existentes con su número de ejercicios. Reutiliza las que encajen antes de inventar otras nuevas.",
			"inputSchema": obj(map[string]any{}), "annotations": readOnly,
		},
		map[string]any{
			"name": "subir_ejercicio", "title": "Subir ejercicio HTML",
			"description": "Publica un ejercicio HTML autocontenido en el catálogo y devuelve su enlace. Si ya existe un fichero con el mismo nombre y tamaño no lo duplica y devuelve el existente.",
			"inputSchema": obj(map[string]any{
				"nombre_fichero": str("Nombre del fichero, p. ej. «sumas-llevando.html». Se añade .html si falta."),
				"html":           str("Documento HTML completo y autocontenido."),
				"titulo":         str("Título visible en el catálogo. Si se omite, se usa el <title> del HTML."),
				"descripcion":    str("Descripción breve para el catálogo."),
				"nivel":          str("Nombre exacto de un nivel existente (ver listar_niveles). Opcional."),
				"etiquetas":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Etiquetas, p. ej. [\"matemáticas\", \"sumas\"]. Se crean si no existen."},
			}, "nombre_fichero", "html"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
		},
		map[string]any{
			"name": "listar_ejercicios", "title": "Listar ejercicios",
			"description": "Devuelve los ejercicios del catálogo con su id, título, descripción, tipo, fichero, tamaño, nivel, etiquetas, fecha y enlace. Todos los filtros son opcionales y se combinan.",
			"inputSchema": obj(map[string]any{
				"texto":    str("Busca en título, descripción, nombre de fichero y etiquetas (sin distinguir mayúsculas ni tildes)."),
				"nivel":    str("Nombre del nivel."),
				"etiqueta": str("Nombre de una etiqueta."),
				"tipo":     map[string]any{"type": "string", "enum": []string{"html", "pdf"}, "description": "Tipo de fichero."},
			}),
			"annotations": readOnly,
		},
		map[string]any{
			"name": "leer_ejercicio", "title": "Leer ejercicio",
			"description": "Devuelve toda la información de un ejercicio y, si es HTML, su código completo. Úsalo antes de actualizar_ejercicio para partir del contenido actual.",
			"inputSchema": obj(map[string]any{"id": map[string]any{"type": "integer", "description": "Id del ejercicio (ver listar_ejercicios)."}}, "id"),
			"annotations": readOnly,
		},
		map[string]any{
			"name": "actualizar_ejercicio", "title": "Actualizar ejercicio",
			"description": "Modifica un ejercicio existente conservando su id y su enlace. Solo cambia los campos que envíes: html sustituye el contenido completo (solo ejercicios HTML), y el resto cambia los datos del catálogo. nivel vacío («») lo deja sin nivel; etiquetas [] quita todas.",
			"inputSchema": obj(map[string]any{
				"id":             map[string]any{"type": "integer", "description": "Id del ejercicio."},
				"html":           str("Nuevo documento HTML completo. Sustituye al actual."),
				"nombre_fichero": str("Nuevo nombre de fichero (solo junto con html)."),
				"titulo":         str("Nuevo título."),
				"descripcion":    str("Nueva descripción."),
				"nivel":          str("Nombre exacto de un nivel existente, o «» para dejarlo sin nivel."),
				"etiquetas":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Lista completa de etiquetas (sustituye a las actuales)."},
			}, "id"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		},
	}
}

func (s *Server) mcpCallTool(r *http.Request, tok store.APIToken, name string, rawArgs json.RawMessage) (string, any, error) {
	switch name {
	case "obtener_instrucciones":
		return exerciseserver.InstruccionesClaude, nil, nil

	case "listar_niveles":
		levels, err := s.store.ListLevels()
		if err != nil {
			return "", nil, err
		}
		type lv struct {
			Nombre     string `json:"nombre"`
			Ejercicios int    `json:"ejercicios"`
		}
		out := []lv{}
		var lines []string
		for _, l := range levels {
			out = append(out, lv{l.Name, l.Count})
			lines = append(lines, fmt.Sprintf("- %s (%d ejercicios)", l.Name, l.Count))
		}
		if len(lines) == 0 {
			return "No hay niveles definidos; sube el ejercicio sin nivel.", map[string]any{"niveles": out}, nil
		}
		return "Niveles:\n" + strings.Join(lines, "\n"), map[string]any{"niveles": out}, nil

	case "listar_etiquetas":
		tags, err := s.store.ListTags()
		if err != nil {
			return "", nil, err
		}
		type tg struct {
			Nombre     string `json:"nombre"`
			Ejercicios int    `json:"ejercicios"`
		}
		out := []tg{}
		var lines []string
		for _, t := range tags {
			out = append(out, tg{t.Name, t.Count})
			lines = append(lines, fmt.Sprintf("- %s (%d)", t.Name, t.Count))
		}
		if len(lines) == 0 {
			return "Todavía no hay etiquetas.", map[string]any{"etiquetas": out}, nil
		}
		return "Etiquetas:\n" + strings.Join(lines, "\n"), map[string]any{"etiquetas": out}, nil

	case "subir_ejercicio":
		var a struct {
			Nombre      string   `json:"nombre_fichero"`
			HTML        string   `json:"html"`
			Titulo      string   `json:"titulo"`
			Descripcion string   `json:"descripcion"`
			Nivel       string   `json:"nivel"`
			Etiquetas   []string `json:"etiquetas"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &a); err != nil {
				return "", nil, fmt.Errorf("argumentos no válidos: %v", err)
			}
		}
		return s.mcpUpload(r, tok, a.Nombre, a.HTML, a.Titulo, a.Descripcion, a.Nivel, a.Etiquetas)

	case "listar_ejercicios":
		var a struct {
			Texto, Nivel, Etiqueta, Tipo string
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &a); err != nil {
				return "", nil, fmt.Errorf("argumentos no válidos: %v", err)
			}
		}
		return s.mcpListFiles(r, a.Texto, a.Nivel, a.Etiqueta, a.Tipo)

	case "leer_ejercicio":
		var a struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rawArgs, &a); err != nil || a.ID <= 0 {
			return "", nil, errors.New("falta un id válido")
		}
		return s.mcpReadFile(r, a.ID)

	case "actualizar_ejercicio":
		var a mcpUpdateArgs
		if err := json.Unmarshal(rawArgs, &a); err != nil || a.ID <= 0 {
			return "", nil, errors.New("falta un id válido o los argumentos no son válidos")
		}
		return s.mcpUpdate(r, tok, a)
	}
	return "", nil, errUnknownTool
}

func (s *Server) mcpUpload(r *http.Request, tok store.APIToken, name, content, title, desc, level string, tags []string) (string, any, error) {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "" || name == "." || name == "/" {
		return "", nil, errors.New("falta nombre_fichero")
	}
	if kindOf(name) != "html" {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".html"
	}
	if err := validateExerciseHTML(content); err != nil {
		return "", nil, err
	}
	levelID, err := s.mcpLevelID(level)
	if err != nil {
		return "", nil, err
	}
	tagList := store.ParseTags(strings.Join(tags, ","))

	link := func(id int64) string { return fmt.Sprintf("%s/ver/%d", baseURL(r), id) }
	if id, dup := s.store.FindFile(name, int64(len(content))); dup {
		f, _ := s.store.GetFile(id)
		return fmt.Sprintf("Ya existía un ejercicio idéntico («%s»); no se ha duplicado.\nEnlace: %s", f.Title, link(id)),
			map[string]any{"id": id, "titulo": f.Title, "url": link(id), "duplicado": true}, nil
	}

	id, err := s.saveFile(name, title, strings.NewReader(content), levelID, tagList, strings.TrimSpace(desc))
	if err != nil {
		return "", nil, err
	}
	f, _ := s.store.GetFile(id)
	log.Printf("mcp: ejercicio %d «%s» subido con el token %d «%s»", id, f.Title, tok.ID, tok.Name)
	return fmt.Sprintf("Ejercicio publicado: «%s».\nEnlace: %s", f.Title, link(id)),
		map[string]any{"id": id, "titulo": f.Title, "url": link(id), "duplicado": false}, nil
}

func validateExerciseHTML(content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("el HTML está vacío")
	}
	if len(content) > mcpMaxHTML {
		return fmt.Errorf("el HTML ocupa demasiado (máximo %s)", humanSize(mcpMaxHTML))
	}
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "<html") && !strings.Contains(lower, "<body") {
		return errors.New("no parece un documento HTML completo (falta <html> o <body>)")
	}
	return nil
}

// mcpLevelID resolves an existing level name; "" means no level. Unknown names are an error listing the valid ones.
func (s *Server) mcpLevelID(level string) (int64, error) {
	if level = strings.TrimSpace(level); level == "" {
		return 0, nil
	}
	id, err := s.store.LevelIDByName(level)
	if errors.Is(err, store.ErrNotFound) {
		levels, _ := s.store.ListLevels()
		names := make([]string, len(levels))
		for i, l := range levels {
			names[i] = "«" + l.Name + "»"
		}
		return 0, fmt.Errorf("el nivel «%s» no existe. Niveles disponibles: %s", level, strings.Join(names, ", "))
	}
	return id, err
}

var foldAccents = strings.NewReplacer("á", "a", "à", "a", "ä", "a", "â", "a", "é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i", "ó", "o", "ò", "o", "ö", "o", "ô", "o", "ú", "u", "ù", "u", "ü", "u", "û", "u", "ñ", "n", "ç", "c")

func foldText(s string) string { return foldAccents.Replace(strings.ToLower(s)) }

type mcpFileInfo struct {
	ID          int64    `json:"id"`
	Titulo      string   `json:"titulo"`
	Descripcion string   `json:"descripcion"`
	Tipo        string   `json:"tipo"`
	Fichero     string   `json:"fichero"`
	Bytes       int64    `json:"bytes"`
	Nivel       string   `json:"nivel"`
	Etiquetas   []string `json:"etiquetas"`
	Subido      string   `json:"subido"`
	URL         string   `json:"url"`
}

func fileInfo(r *http.Request, f store.File) mcpFileInfo {
	tags := f.Tags
	if tags == nil {
		tags = []string{}
	}
	return mcpFileInfo{ID: f.ID, Titulo: f.Title, Descripcion: f.Description, Tipo: f.Kind, Fichero: f.OrigName,
		Bytes: f.Size, Nivel: f.LevelName, Etiquetas: tags, Subido: f.UploadedAt.Format("2006-01-02T15:04:05Z07:00"),
		URL: fmt.Sprintf("%s/ver/%d", baseURL(r), f.ID)}
}

func (i mcpFileInfo) line() string {
	nivel := i.Nivel
	if nivel == "" {
		nivel = "sin nivel"
	}
	l := fmt.Sprintf("- [%d] %s (%s, %s, %s)", i.ID, i.Titulo, i.Tipo, nivel, humanSize(i.Bytes))
	if len(i.Etiquetas) > 0 {
		l += " · etiquetas: " + strings.Join(i.Etiquetas, ", ")
	}
	if i.Descripcion != "" {
		l += "\n  " + i.Descripcion
	}
	return l
}

func (s *Server) mcpListFiles(r *http.Request, text, level, tag, kind string) (string, any, error) {
	files, err := s.store.ListFiles()
	if err != nil {
		return "", nil, err
	}
	text, level, tag, kind = foldText(strings.TrimSpace(text)), foldText(strings.TrimSpace(level)), foldText(strings.TrimSpace(tag)), strings.ToLower(strings.TrimSpace(kind))
	out := []mcpFileInfo{}
	var lines []string
	for _, f := range files {
		if kind != "" && f.Kind != kind {
			continue
		}
		if level != "" && foldText(f.LevelName) != level {
			continue
		}
		if tag != "" {
			found := false
			for _, t := range f.Tags {
				found = found || foldText(t) == tag
			}
			if !found {
				continue
			}
		}
		if text != "" && !strings.Contains(foldText(f.Title+"\n"+f.Description+"\n"+f.OrigName+"\n"+strings.Join(f.Tags, "\n")), text) {
			continue
		}
		info := fileInfo(r, f)
		out = append(out, info)
		lines = append(lines, info.line())
	}
	structured := map[string]any{"total": len(out), "ejercicios": out}
	if len(out) == 0 {
		return "No hay ejercicios que coincidan.", structured, nil
	}
	return fmt.Sprintf("%d ejercicio(s):\n%s", len(out), strings.Join(lines, "\n")), structured, nil
}

func (s *Server) readStored(f store.File) (string, error) {
	fh, err := os.Open(filepath.Join(s.filesDir, filepath.Base(f.StoredName)))
	if err != nil {
		return "", fmt.Errorf("no se puede leer el fichero del ejercicio %d", f.ID)
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, mcpMaxHTML+1))
	if err != nil {
		return "", err
	}
	if len(b) > mcpMaxHTML {
		return "", fmt.Errorf("el ejercicio %d ocupa demasiado para leerlo por MCP", f.ID)
	}
	return string(b), nil
}

func (s *Server) mcpReadFile(r *http.Request, id int64) (string, any, error) {
	f, err := s.store.GetFile(id)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil, fmt.Errorf("no existe ningún ejercicio con id %d", id)
	}
	if err != nil {
		return "", nil, err
	}
	info := fileInfo(r, f)
	structured := map[string]any{"ejercicio": info}
	head := fmt.Sprintf("Ejercicio %d: %s\nFichero: %s (%s, %s)\nNivel: %s\nEtiquetas: %s\nDescripción: %s\nSubido: %s\nEnlace: %s",
		info.ID, info.Titulo, info.Fichero, info.Tipo, humanSize(info.Bytes), orDash(info.Nivel),
		orDash(strings.Join(info.Etiquetas, ", ")), orDash(info.Descripcion), info.Subido, info.URL)
	if f.Kind != "html" {
		return head + "\n\n(Es un PDF: su contenido no se devuelve por MCP.)", structured, nil
	}
	content, err := s.readStored(f)
	if err != nil {
		return "", nil, err
	}
	structured["html"] = content
	return head + "\n\n--- HTML ---\n" + content, structured, nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// mcpUpdateArgs uses pointers so that "absent" (keep) differs from "empty" (clear).
type mcpUpdateArgs struct {
	ID          int64     `json:"id"`
	HTML        *string   `json:"html"`
	Nombre      *string   `json:"nombre_fichero"`
	Titulo      *string   `json:"titulo"`
	Descripcion *string   `json:"descripcion"`
	Nivel       *string   `json:"nivel"`
	Etiquetas   *[]string `json:"etiquetas"`
}

func (s *Server) mcpUpdate(r *http.Request, tok store.APIToken, a mcpUpdateArgs) (string, any, error) {
	f, err := s.store.GetFile(a.ID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil, fmt.Errorf("no existe ningún ejercicio con id %d", a.ID)
	}
	if err != nil {
		return "", nil, err
	}
	if a.HTML == nil && a.Nombre == nil && a.Titulo == nil && a.Descripcion == nil && a.Nivel == nil && a.Etiquetas == nil {
		return "", nil, errors.New("no has indicado nada que cambiar")
	}

	// Validate everything before touching anything, so a bad argument never leaves a half-applied update.
	title, desc, levelID, tags := f.Title, f.Description, f.LevelID.Int64, f.Tags
	if a.Titulo != nil {
		if title = strings.TrimSpace(*a.Titulo); title == "" {
			return "", nil, errors.New("el título no puede estar vacío")
		}
	}
	if a.Descripcion != nil {
		desc = strings.TrimSpace(*a.Descripcion)
	}
	if a.Nivel != nil {
		if levelID, err = s.mcpLevelID(*a.Nivel); err != nil {
			return "", nil, err
		}
	}
	if a.Etiquetas != nil {
		tags = store.ParseTags(strings.Join(*a.Etiquetas, ","))
	}
	name := f.OrigName
	if a.Nombre != nil {
		if a.HTML == nil {
			return "", nil, errors.New("nombre_fichero solo se puede cambiar junto con html")
		}
		name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(*a.Nombre), "\\", "/"))
		if name == "" || name == "." || name == "/" {
			return "", nil, errors.New("nombre_fichero no válido")
		}
		if kindOf(name) != "html" {
			name = strings.TrimSuffix(name, filepath.Ext(name)) + ".html"
		}
	}
	if a.HTML != nil {
		if f.Kind != "html" {
			return "", nil, errors.New("este ejercicio es un PDF; solo se puede cambiar el contenido de los ejercicios HTML")
		}
		if err := validateExerciseHTML(*a.HTML); err != nil {
			return "", nil, err
		}
		if kindOf(name) != "html" {
			name = strings.TrimSuffix(name, filepath.Ext(name)) + ".html"
		}
	}

	var changed []string
	if a.HTML != nil {
		if err := s.replaceContent(f.ID, name, strings.NewReader(*a.HTML)); err != nil {
			return "", nil, err
		}
		changed = append(changed, "contenido")
	}
	if err := s.store.UpdateFile(f.ID, title, desc, levelID, tags); err != nil {
		return "", nil, err
	}
	for _, c := range []struct {
		set  bool
		name string
	}{{a.Titulo != nil, "título"}, {a.Descripcion != nil, "descripción"}, {a.Nivel != nil, "nivel"}, {a.Etiquetas != nil, "etiquetas"}} {
		if c.set {
			changed = append(changed, c.name)
		}
	}
	updated, _ := s.store.GetFile(f.ID)
	info := fileInfo(r, updated)
	log.Printf("mcp: ejercicio %d «%s» actualizado (%s) con el token %d «%s»", f.ID, updated.Title, strings.Join(changed, ", "), tok.ID, tok.Name)
	return fmt.Sprintf("Ejercicio %d actualizado (%s).\n%s\nEnlace: %s", f.ID, strings.Join(changed, ", "), info.line(), info.URL),
		map[string]any{"ejercicio": info, "cambiado": changed}, nil
}
