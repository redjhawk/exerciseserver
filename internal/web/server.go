// Package web serves the exercise catalogue and the admin interface.
package web

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redjhawk/exerciseserver/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const (
	cookieName     = "exs_session"
	sessionTTL     = 30 * 24 * time.Hour
	maxUploadBytes = 200 << 20
)

type Server struct {
	store    *store.Store
	filesDir string
	version  string
	pages    map[string]*template.Template
	limiter  *loginLimiter
}

func New(st *store.Store, dataDir, version string) (*Server, error) {
	filesDir := filepath.Join(dataDir, "files")
	if err := os.MkdirAll(filesDir, 0o750); err != nil {
		return nil, err
	}
	s := &Server{store: st, filesDir: filesDir, version: version, pages: map[string]*template.Template{},
		limiter: &loginLimiter{fails: map[string][]time.Time{}}}
	funcs := template.FuncMap{
		"join":  strings.Join,
		"size":  humanSize,
		"date":  func(t time.Time) string { return t.Format("02/01/2006") },
		"lower": strings.ToLower,
		"tagsAttr": func(tags []string) string {
			l := make([]string, len(tags))
			for i, t := range tags {
				l[i] = strings.ToLower(t)
			}
			return strings.Join(l, "|")
		},
	}
	names, _ := fs.Glob(templateFS, "templates/*.html")
	for _, n := range names {
		base := filepath.Base(n)
		if base == "layout.html" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", n)
		if err != nil {
			return nil, err
		}
		s.pages[strings.TrimSuffix(base, ".html")] = t
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })

	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.logout)

	mux.HandleFunc("GET /{$}", s.auth(s.catalog))
	mux.HandleFunc("GET /ver/{id}", s.auth(s.viewer))
	mux.HandleFunc("GET /f/{id}/contenido", s.auth(s.rawFile))
	mux.HandleFunc("GET /f/{id}/descargar", s.auth(s.downloadFile))

	mux.HandleFunc("GET /admin", s.admin(func(w http.ResponseWriter, r *http.Request, _ store.Session) {
		http.Redirect(w, r, "/admin/ficheros", http.StatusSeeOther)
	}))
	mux.HandleFunc("GET /admin/ficheros", s.admin(s.adminFiles))
	mux.HandleFunc("POST /admin/ficheros", s.admin(s.adminUpload))
	mux.HandleFunc("GET /admin/ficheros/{id}", s.admin(s.adminFileEdit))
	mux.HandleFunc("POST /admin/ficheros/{id}", s.admin(s.adminFileUpdate))
	mux.HandleFunc("POST /admin/ficheros/{id}/borrar", s.admin(s.adminFileDelete))

	mux.HandleFunc("GET /admin/usuarios", s.admin(s.adminUsers))
	mux.HandleFunc("POST /admin/usuarios", s.admin(s.adminUserCreate))
	mux.HandleFunc("POST /admin/usuarios/{id}", s.admin(s.adminUserFlags))
	mux.HandleFunc("POST /admin/usuarios/{id}/password", s.admin(s.adminUserPassword))
	mux.HandleFunc("POST /admin/usuarios/{id}/borrar", s.admin(s.adminUserDelete))

	mux.HandleFunc("GET /admin/niveles", s.admin(s.adminLevels))
	mux.HandleFunc("POST /admin/niveles", s.admin(s.adminLevelCreate))
	mux.HandleFunc("POST /admin/niveles/{id}", s.admin(s.adminLevelUpdate))
	mux.HandleFunc("POST /admin/niveles/{id}/borrar", s.admin(s.adminLevelDelete))

	mux.HandleFunc("GET /admin/etiquetas", s.admin(s.adminTags))
	mux.HandleFunc("POST /admin/etiquetas/{id}", s.admin(s.adminTagRename))
	mux.HandleFunc("POST /admin/etiquetas/{id}/borrar", s.admin(s.adminTagDelete))

	return securityHeaders(sameOrigin(mux))
}

// ---------- middleware ----------

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/f/") {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'; object-src 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects cross-site state-changing requests (defence in depth on top of CSRF tokens).
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" && o != "null" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					http.Error(w, "origen no permitido", http.StatusForbidden)
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "origen no permitido", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

type authedHandler func(http.ResponseWriter, *http.Request, store.Session)

func (s *Server) session(r *http.Request) (store.Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return store.Session{}, false
	}
	return s.store.GetSession(c.Value)
}

func (s *Server) auth(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.session(r)
		if !ok {
			next := r.URL.RequestURI()
			if strings.HasPrefix(r.URL.Path, "/f/") {
				next = "/"
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
			return
		}
		h(w, r, sess)
	}
}

func (s *Server) admin(h authedHandler) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, sess store.Session) {
		if !sess.User.IsAdmin {
			http.Error(w, "solo para administradores", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				redirectMsg(w, r, r.URL.Path, errors.New("subida demasiado grande o inválida"), "")
				return
			}
			defer r.MultipartForm.RemoveAll()
		}
		if r.Method == http.MethodPost && !s.checkCSRF(r, sess) {
			http.Error(w, "token CSRF inválido; recarga la página", http.StatusForbidden)
			return
		}
		h(w, r, sess)
	})
}

func (s *Server) checkCSRF(r *http.Request, sess store.Session) bool {
	tok := r.FormValue("csrf")
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(sess.CSRFToken)) == 1
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// clientIP trusts CF-Connecting-IP only when the request comes from a local/private address (the tunnel).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
			return cf
		}
	}
	return host
}

// ---------- rendering ----------

type pageData struct {
	Title   string
	Session store.Session
	Version string
	Flash   string
	Error   string
	Active  string
	Data    any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, d pageData) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "plantilla no encontrada", 500)
		return
	}
	d.Version = s.version
	if d.Flash == "" {
		d.Flash = r.URL.Query().Get("ok")
	}
	if d.Error == "" {
		d.Error = r.URL.Query().Get("err")
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "error interno", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	buf.WriteTo(w)
}

func redirectMsg(w http.ResponseWriter, r *http.Request, path string, err error, ok string) {
	q := url.Values{}
	if err != nil {
		q.Set("err", err.Error())
	} else if ok != "" {
		q.Set("ok", ok)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ---------- login ----------

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

const (
	loginWindow   = 15 * time.Minute
	loginMaxFails = 10
)

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-loginWindow)
	var keep []time.Time
	for _, t := range l.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = keep
	}
	return len(keep) >= loginMaxFails
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	l.fails[ip] = append(l.fails[ip], time.Now())
	l.mu.Unlock()
}

func safeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.HasPrefix(n, "/\\") {
		return "/"
	}
	return n
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.session(r); ok {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", pageData{Title: "Entrar", Data: map[string]string{"Next": safeNext(r.URL.Query().Get("next"))}})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	next := safeNext(r.FormValue("next"))
	if s.limiter.blocked(ip) {
		s.render(w, r, "login", pageData{Title: "Entrar", Error: "Demasiados intentos. Espera unos minutos.",
			Data: map[string]string{"Next": next}})
		return
	}
	u, ok := s.store.Authenticate(r.FormValue("username"), r.FormValue("password"))
	if !ok {
		s.limiter.fail(ip)
		log.Printf("login fallido para %q desde %s", r.FormValue("username"), ip)
		s.render(w, r, "login", pageData{Title: "Entrar", Error: "Usuario o contraseña incorrectos.",
			Data: map[string]string{"Next": next}})
		return
	}
	tok, err := s.store.CreateSession(u.ID, sessionTTL)
	if err != nil {
		http.Error(w, "error interno", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.session(r); ok {
		if !s.checkCSRF(r, sess) {
			http.Error(w, "token CSRF inválido", http.StatusForbidden)
			return
		}
		c, _ := r.Cookie(cookieName)
		s.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------- catalogue ----------

type catalogData struct {
	Files  []store.File
	Levels []store.Level
	Tags   []store.Tag
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request, sess store.Session) {
	files, err := s.store.ListFiles()
	if err != nil {
		http.Error(w, "error interno", 500)
		return
	}
	levels, _ := s.store.ListLevels()
	tags, _ := s.store.ListTags()
	var usedLevels []store.Level
	for _, l := range levels {
		if l.Count > 0 {
			usedLevels = append(usedLevels, l)
		}
	}
	s.render(w, r, "catalog", pageData{Title: "Ejercicios", Session: sess, Active: "catalog",
		Data: catalogData{Files: files, Levels: usedLevels, Tags: tags}})
}

func (s *Server) viewer(w http.ResponseWriter, r *http.Request, sess store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := s.store.GetFile(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "viewer", pageData{Title: f.Title, Session: sess, Active: "viewer", Data: f})
}

func (s *Server) openStored(w http.ResponseWriter, r *http.Request) (store.File, *os.File, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return store.File{}, nil, false
	}
	f, err := s.store.GetFile(id)
	if err != nil {
		http.NotFound(w, r)
		return f, nil, false
	}
	fh, err := os.Open(filepath.Join(s.filesDir, filepath.Base(f.StoredName)))
	if err != nil {
		log.Printf("abrir %s: %v", f.StoredName, err)
		http.NotFound(w, r)
		return f, nil, false
	}
	return f, fh, true
}

// The HTML exercises run in a sandbox with an opaque origin: their scripts work,
// but they cannot read the session cookie or call the admin endpoints.
const htmlSandboxCSP = "sandbox allow-scripts allow-forms allow-modals allow-popups allow-downloads; frame-ancestors 'self'"

func (s *Server) rawFile(w http.ResponseWriter, r *http.Request, _ store.Session) {
	f, fh, ok := s.openStored(w, r)
	if !ok {
		return
	}
	defer fh.Close()
	st, _ := fh.Stat()
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	if f.Kind == "html" {
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", htmlSandboxCSP)
		h.Set("X-Frame-Options", "SAMEORIGIN")
	} else {
		h.Set("Content-Type", "application/pdf")
		h.Set("Content-Disposition", contentDisposition("inline", f))
	}
	http.ServeContent(w, r, "", st.ModTime(), fh)
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request, _ store.Session) {
	f, fh, ok := s.openStored(w, r)
	if !ok {
		return
	}
	defer fh.Close()
	st, _ := fh.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", f))
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", st.ModTime(), fh)
}

func contentDisposition(kind string, f store.File) string {
	name := f.OrigName
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, kind, ascii, url.PathEscape(name))
}

// ---------- admin: files ----------

type adminFilesData struct {
	Files  []store.File
	Levels []store.Level
	Tags   []store.Tag
}

func (s *Server) adminFiles(w http.ResponseWriter, r *http.Request, sess store.Session) {
	files, err := s.store.ListFiles()
	if err != nil {
		http.Error(w, "error interno", 500)
		return
	}
	levels, _ := s.store.ListLevels()
	tags, _ := s.store.ListTags()
	s.render(w, r, "admin_files", pageData{Title: "Ficheros", Session: sess, Active: "files",
		Data: adminFilesData{Files: files, Levels: levels, Tags: tags}})
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func guessTitle(origName string, head []byte, kind string) string {
	if kind == "html" {
		if m := titleRe.FindSubmatch(head); m != nil {
			t := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
			if t != "" {
				return t
			}
		}
	}
	base := strings.TrimSuffix(origName, filepath.Ext(origName))
	base = strings.NewReplacer("_", " ", "-", " ").Replace(base)
	base = strings.Join(strings.Fields(base), " ")
	if base == "" {
		return origName
	}
	return base
}

func kindOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return "html"
	case ".pdf":
		return "pdf"
	}
	return ""
}

func randomName(ext string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b) + ext
}

// SaveFile copies r into the files directory, validates it and records it in the store.
func (s *Server) SaveFile(origName string, r io.Reader, levelID int64, tags []string, description string) (int64, error) {
	origName = filepath.Base(strings.ReplaceAll(origName, "\\", "/"))
	kind := kindOf(origName)
	if kind == "" {
		return 0, fmt.Errorf("%s: solo se admiten ficheros .html, .htm y .pdf", origName)
	}
	stored := randomName("." + kind)
	dst := filepath.Join(s.filesDir, stored)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, err
	}
	head := make([]byte, 64<<10)
	nHead, _ := io.ReadFull(r, head)
	head = head[:nHead]
	n, err := io.Copy(out, io.MultiReader(bytes.NewReader(head), r))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return 0, err
	}
	if kind == "pdf" && !bytes.HasPrefix(bytes.TrimLeft(head, "\x00\t\r\n "), []byte("%PDF-")) {
		os.Remove(dst)
		return 0, fmt.Errorf("%s: no parece un PDF válido", origName)
	}
	id, err := s.store.CreateFile(store.NewFile{
		Title: guessTitle(origName, head, kind), Description: description, Kind: kind,
		OrigName: origName, StoredName: stored, Size: n, LevelID: levelID, Tags: tags,
	})
	if err != nil {
		os.Remove(dst)
		return 0, err
	}
	return id, nil
}

func (s *Server) levelFromForm(r *http.Request) (int64, error) {
	if nl := strings.TrimSpace(r.FormValue("new_level")); nl != "" {
		return s.store.LevelByName(nl)
	}
	id, _ := strconv.ParseInt(r.FormValue("level_id"), 10, 64)
	return id, nil
}

func (s *Server) adminUpload(w http.ResponseWriter, r *http.Request, _ store.Session) {
	if r.MultipartForm == nil {
		redirectMsg(w, r, "/admin/ficheros", errors.New("formulario inválido"), "")
		return
	}
	levelID, err := s.levelFromForm(r)
	if err != nil {
		redirectMsg(w, r, "/admin/ficheros", err, "")
		return
	}
	tags := store.ParseTags(r.FormValue("tags"))
	desc := strings.TrimSpace(r.FormValue("description"))
	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		redirectMsg(w, r, "/admin/ficheros", errors.New("no has seleccionado ningún fichero"), "")
		return
	}
	var errs []string
	okCount := 0
	for _, fh := range headers {
		if err := s.saveUpload(fh, levelID, tags, desc); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		okCount++
	}
	if len(errs) > 0 {
		redirectMsg(w, r, "/admin/ficheros", fmt.Errorf("subidos %d; errores: %s", okCount, strings.Join(errs, "; ")), "")
		return
	}
	redirectMsg(w, r, "/admin/ficheros", nil, fmt.Sprintf("%d fichero(s) subido(s)", okCount))
}

func (s *Server) saveUpload(fh *multipart.FileHeader, levelID int64, tags []string, desc string) error {
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = s.SaveFile(fh.Filename, f, levelID, tags, desc)
	return err
}

type fileEditData struct {
	File   store.File
	Levels []store.Level
	Tags   []store.Tag
}

func (s *Server) adminFileEdit(w http.ResponseWriter, r *http.Request, sess store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := s.store.GetFile(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	levels, _ := s.store.ListLevels()
	tags, _ := s.store.ListTags()
	s.render(w, r, "admin_file_edit", pageData{Title: "Editar " + f.Title, Session: sess, Active: "files",
		Data: fileEditData{File: f, Levels: levels, Tags: tags}})
}

func (s *Server) adminFileUpdate(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	back := fmt.Sprintf("/admin/ficheros/%d", id)
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		redirectMsg(w, r, back, errors.New("el título no puede estar vacío"), "")
		return
	}
	levelID, err := s.levelFromForm(r)
	if err == nil {
		err = s.store.UpdateFile(id, title, strings.TrimSpace(r.FormValue("description")), levelID, store.ParseTags(r.FormValue("tags")))
	}
	if err != nil {
		redirectMsg(w, r, back, err, "")
		return
	}
	redirectMsg(w, r, "/admin/ficheros", nil, "Guardado: "+title)
}

func (s *Server) adminFileDelete(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	stored, err := s.store.DeleteFile(id)
	if err == nil {
		os.Remove(filepath.Join(s.filesDir, filepath.Base(stored)))
	}
	redirectMsg(w, r, "/admin/ficheros", err, "Fichero borrado")
}

// ---------- admin: users ----------

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request, sess store.Session) {
	users, err := s.store.ListUsers()
	if err != nil {
		http.Error(w, "error interno", 500)
		return
	}
	s.render(w, r, "admin_users", pageData{Title: "Usuarios", Session: sess, Active: "users", Data: users})
}

func (s *Server) adminUserCreate(w http.ResponseWriter, r *http.Request, _ store.Session) {
	err := s.store.CreateUser(r.FormValue("username"), r.FormValue("password"), r.FormValue("is_admin") == "1")
	redirectMsg(w, r, "/admin/usuarios", err, "Usuario creado")
}

// guardLastAdmin refuses changes that would leave no active administrator.
func (s *Server) guardLastAdmin(id int64, stillAdmin bool) error {
	u, err := s.store.GetUser(id)
	if err != nil {
		return err
	}
	if u.IsAdmin && u.Active && !stillAdmin {
		n, err := s.store.CountActiveAdmins()
		if err != nil {
			return err
		}
		if n <= 1 {
			return errors.New("debe quedar al menos un administrador activo")
		}
	}
	return nil
}

func (s *Server) adminUserFlags(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	active := r.FormValue("active") == "1"
	admin := r.FormValue("is_admin") == "1"
	err := s.guardLastAdmin(id, active && admin)
	if err == nil {
		err = s.store.SetUserFlags(id, active, admin)
	}
	redirectMsg(w, r, "/admin/usuarios", err, "Usuario actualizado")
}

func (s *Server) adminUserPassword(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.store.SetUserPassword(id, r.FormValue("password"))
	redirectMsg(w, r, "/admin/usuarios", err, "Contraseña cambiada")
}

func (s *Server) adminUserDelete(w http.ResponseWriter, r *http.Request, sess store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if id == sess.User.ID {
		redirectMsg(w, r, "/admin/usuarios", errors.New("no puedes borrar tu propio usuario"), "")
		return
	}
	err := s.guardLastAdmin(id, false)
	if err == nil {
		err = s.store.DeleteUser(id)
	}
	redirectMsg(w, r, "/admin/usuarios", err, "Usuario borrado")
}

// ---------- admin: levels ----------

func (s *Server) adminLevels(w http.ResponseWriter, r *http.Request, sess store.Session) {
	levels, err := s.store.ListLevels()
	if err != nil {
		http.Error(w, "error interno", 500)
		return
	}
	s.render(w, r, "admin_levels", pageData{Title: "Niveles", Session: sess, Active: "levels", Data: levels})
}

func (s *Server) adminLevelCreate(w http.ResponseWriter, r *http.Request, _ store.Session) {
	pos, _ := strconv.Atoi(r.FormValue("position"))
	err := s.store.CreateLevel(r.FormValue("name"), pos)
	redirectMsg(w, r, "/admin/niveles", err, "Nivel creado")
}

func (s *Server) adminLevelUpdate(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	pos, _ := strconv.Atoi(r.FormValue("position"))
	err := s.store.UpdateLevel(id, r.FormValue("name"), pos)
	redirectMsg(w, r, "/admin/niveles", err, "Nivel guardado")
}

func (s *Server) adminLevelDelete(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.store.DeleteLevel(id)
	redirectMsg(w, r, "/admin/niveles", err, "Nivel borrado (sus ficheros quedan sin nivel)")
}

// ---------- admin: tags ----------

func (s *Server) adminTags(w http.ResponseWriter, r *http.Request, sess store.Session) {
	tags, err := s.store.ListTags()
	if err != nil {
		http.Error(w, "error interno", 500)
		return
	}
	s.render(w, r, "admin_tags", pageData{Title: "Etiquetas", Session: sess, Active: "tags", Data: tags})
}

func (s *Server) adminTagRename(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.store.RenameTag(id, r.FormValue("name"))
	redirectMsg(w, r, "/admin/etiquetas", err, "Etiqueta guardada")
}

func (s *Server) adminTagDelete(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.store.DeleteTag(id)
	redirectMsg(w, r, "/admin/etiquetas", err, "Etiqueta borrada")
}
