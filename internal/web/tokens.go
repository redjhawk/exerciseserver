package web

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/redjhawk/exerciseserver"
	"github.com/redjhawk/exerciseserver/internal/store"
)

// tokenDurations are the lifetimes offered when creating an API token, in display order.
var tokenDurations = []struct {
	Value string
	Label string
	TTL   time.Duration
}{
	{"1h", "1 hora", time.Hour},
	{"1d", "1 día", 24 * time.Hour},
	{"7d", "7 días", 7 * 24 * time.Hour},
	{"30d", "30 días", 30 * 24 * time.Hour},
	{"never", "Sin caducidad", 0},
}

type tokenRow struct {
	store.APIToken
	Status string
}

type adminTokensData struct {
	Tokens    []tokenRow
	Durations any
	BaseURL   string
	// Set only right after creating a token: the raw value is shown once and never again.
	NewToken     string
	NewTokenName string
	NewTokenExp  time.Time
}

// baseURL is the public origin of this server as seen by the client (honours the Cloudflare tunnel).
func baseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) renderTokens(w http.ResponseWriter, r *http.Request, sess store.Session, d adminTokensData) {
	list, err := s.store.ListAPITokens()
	if err != nil {
		http.Error(w, "error interno", 500)
		return
	}
	now := time.Now()
	for _, t := range list {
		d.Tokens = append(d.Tokens, tokenRow{APIToken: t, Status: t.Status(now)})
	}
	d.Durations = tokenDurations
	d.BaseURL = baseURL(r)
	s.render(w, r, "admin_tokens", pageData{Title: "Tokens", Session: sess, Active: "tokens", Data: d})
}

func (s *Server) adminTokens(w http.ResponseWriter, r *http.Request, sess store.Session) {
	s.renderTokens(w, r, sess, adminTokensData{})
}

func (s *Server) adminTokenCreate(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var ttl time.Duration
	found := false
	for _, d := range tokenDurations {
		if d.Value == r.FormValue("duration") {
			ttl, found = d.TTL, true
		}
	}
	if !found {
		redirectMsg(w, r, "/admin/tokens", errors.New("duración no válida"), "")
		return
	}
	raw, tok, err := s.store.CreateAPIToken(r.FormValue("name"), ttl)
	if err != nil {
		redirectMsg(w, r, "/admin/tokens", err, "")
		return
	}
	// Rendered directly (not redirected) so the raw token never ends up in a URL or the history.
	s.renderTokens(w, r, sess, adminTokensData{NewToken: raw, NewTokenName: tok.Name, NewTokenExp: tok.ExpiresAt})
}

func (s *Server) adminTokenRevoke(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	redirectMsg(w, r, "/admin/tokens", s.store.RevokeAPIToken(id), "Token revocado")
}

func (s *Server) adminTokenDelete(w http.ResponseWriter, r *http.Request, _ store.Session) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.store.DeleteAPIToken(id)
	if errors.Is(err, store.ErrNotFound) {
		err = errors.New("ese token no existe")
	}
	redirectMsg(w, r, "/admin/tokens", err, "Token borrado")
}

// apiTokenFromRequest reads the token from the /mcp/{token} path or from "Authorization: Bearer".
func apiTokenFromRequest(r *http.Request) string {
	if t := strings.TrimSpace(r.PathValue("token")); t != "" {
		return t
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// instructionsFile serves INSTRUCCIONES_CLAUDE.md as embedded in this binary (?descargar=1 to download it).
func (s *Server) instructionsFile(w http.ResponseWriter, r *http.Request, _ store.Session) {
	h := w.Header()
	h.Set("Content-Type", "text/markdown; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	if r.URL.Query().Get("descargar") != "" {
		h.Set("Content-Disposition", `attachment; filename="INSTRUCCIONES_CLAUDE.md"`)
	} else {
		// Shown as plain text in the browser (no markdown renderer needed).
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	io.WriteString(w, exerciseserver.InstruccionesClaude)
}

// instructionsWithToken serves the same document to an AI that only has an API token
// (in the path, /mcp/<token>/instrucciones.md, or as a Bearer header on /mcp/instrucciones.md).
func (s *Server) instructionsWithToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.store.ValidateAPIToken(apiTokenFromRequest(r)); !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="exerciseserver"`)
		http.Error(w, "token no válido, caducado o revocado", http.StatusUnauthorized)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/markdown; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")
	io.WriteString(w, exerciseserver.InstruccionesClaude)
}
