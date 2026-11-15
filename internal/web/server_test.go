package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redjhawk/exerciseserver/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

func client(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func login(t *testing.T, c *http.Client, base, user, pw string) {
	t.Helper()
	resp, err := c.PostForm(base+"/login", url.Values{"username": {user}, "password": {pw}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("login %s: %d %s", user, resp.StatusCode, resp.Header.Get("Location"))
	}
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func csrfToken(t *testing.T, c *http.Client, base string) string {
	resp, err := c.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	m := csrfRe.FindStringSubmatch(b.String())
	if m == nil {
		t.Fatal("no csrf token in page")
	}
	return m[1]
}

func TestFlow(t *testing.T) {
	ts, st := newTestServer(t)
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}

	// anonymous users are sent to the login page
	anon := client(t)
	resp, _ := anon.Get(ts.URL + "/")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("anon: %d", resp.StatusCode)
	}

	// bad password is rejected
	resp, _ = anon.PostForm(ts.URL+"/login", url.Values{"username": {"nino"}, "password": {"nope"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bad login: %d", resp.StatusCode)
	}

	admin := client(t)
	login(t, admin, ts.URL, "admin", "secret-admin")
	tok := csrfToken(t, admin, ts.URL)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", tok)
	mw.WriteField("tags", "Matemáticas, sumas, matemáticas")
	mw.WriteField("new_level", "1º")
	fw, _ := mw.CreateFormFile("files", "sumas.html")
	fw.Write([]byte("<title>Sumas</title><script>alert(1)</script>"))
	fw, _ = mw.CreateFormFile("files", "malo.pdf")
	fw.Write([]byte("not a pdf"))
	fw, _ = mw.CreateFormFile("files", "virus.exe")
	fw.Write([]byte("MZ"))
	mw.Close()
	resp, err := admin.Post(ts.URL+"/admin/ficheros", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if e := loc.Query().Get("err"); !strings.Contains(e, "subidos 1") || !strings.Contains(e, "malo.pdf") || !strings.Contains(e, "virus.exe") {
		t.Fatalf("upload result: %q", loc)
	}

	files, _ := st.ListFiles()
	if len(files) != 1 || files[0].Title != "Sumas" || files[0].LevelName != "1º" || len(files[0].Tags) != 2 {
		t.Fatalf("files: %+v", files)
	}

	// a regular user can view but not administer
	kid := client(t)
	login(t, kid, ts.URL, "nino", "1234")
	resp, _ = kid.Get(ts.URL + "/f/1/contenido")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("raw: %d %q", resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}
	resp, _ = kid.Get(ts.URL + "/admin/usuarios")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("kid admin: %d", resp.StatusCode)
	}

	// POST without CSRF token is refused
	resp, _ = admin.PostForm(ts.URL+"/admin/usuarios", url.Values{"username": {"x"}, "password": {"12345"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no csrf: %d", resp.StatusCode)
	}

	// the last admin cannot be demoted
	resp, _ = admin.PostForm(ts.URL+"/admin/usuarios/1", url.Values{"csrf": {tok}, "active": {"1"}})
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "err=") {
		t.Fatalf("demote last admin: %s", loc)
	}
}

func TestReplaceUploadedFileKeepsSameRecord(t *testing.T) {
	ts, st := newTestServer(t)
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	admin := client(t)
	login(t, admin, ts.URL, "admin", "secret-admin")
	csrf := csrfToken(t, admin, ts.URL)

	var upload bytes.Buffer
	mw := multipart.NewWriter(&upload)
	mw.WriteField("csrf", csrf)
	mw.WriteField("tags", "matemáticas")
	fw, err := mw.CreateFormFile("files", "sumas.html")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("<html><head><title>Sumas</title></head><body>viejo</body></html>"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := admin.Post(ts.URL+"/admin/ficheros", mw.FormDataContentType(), &upload)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("upload status: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	files, err := st.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	id := files[0].ID

	var replace bytes.Buffer
	mw = multipart.NewWriter(&replace)
	mw.WriteField("csrf", csrf)
	mw.WriteField("title", "Sumas actualizadas")
	mw.WriteField("description", "Versión nueva")
	mw.WriteField("tags", "matemáticas, suma")
	fw, err = mw.CreateFormFile("replacement_file", "sumas-v2.html")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("<html><head><title>Sumas actualizadas</title></head><body>nuevo contenido</body></html>"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err = admin.Post(ts.URL+"/admin/ficheros/"+url.QueryEscape(strconv.FormatInt(id, 10)), mw.FormDataContentType(), &replace)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("replace status: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	f, err := st.GetFile(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Sumas actualizadas" {
		t.Fatalf("title not updated: %+v", f)
	}
	resp, err = admin.Get(ts.URL + "/f/" + strconv.FormatInt(id, 10) + "/contenido")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "nuevo contenido") {
		t.Fatalf("replacement content not served: %s", string(body))
	}
	if !strings.Contains(string(body), "Sumas actualizadas") {
		t.Fatalf("updated title not rendered: %s", string(body))
	}
}

func TestUserAIProviderAndProxy(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	fakeClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "generativelanguage.googleapis.com" {
			t.Fatalf("unexpected host: %s", req.URL.Host)
		}
		body := `{"candidates":[{"content":{"parts":[{"text":"respuesta OK"}]}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	srv.httpClient = fakeClient
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := client(t)
	login(t, admin, ts.URL, "admin", "secret-admin")
	csrf := csrfToken(t, admin, ts.URL)
	resp, err := admin.PostForm(ts.URL+"/admin/usuarios/2/ia", url.Values{
		"csrf":     {csrf},
		"provider": {"gemini"},
		"api_key":  {"super-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save AI config: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	cfg, err := st.GetUserAI(2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "gemini" || cfg.APIKey != "super-secret" {
		t.Fatalf("stored cfg: %+v", cfg)
	}

	kid := client(t)
	login(t, kid, ts.URL, "nino", "1234")
	resp, err = kid.Post(ts.URL+"/api/ia", "application/json", strings.NewReader(`{"prompt":"responde con una frase"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AI proxy: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "respuesta OK") {
		t.Fatalf("proxy response: %s", string(body))
	}
}

func TestUserCanSetOwnAIAndAdminCanForceIt(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	kid := client(t)
	login(t, kid, ts.URL, "nino", "1234")
	csrf := csrfToken(t, kid, ts.URL)
	resp, err := kid.PostForm(ts.URL+"/mi-ia", url.Values{"csrf": {csrf}, "provider": {"gemini"}, "api_key": {"user-key"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("user save AI: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	cfg, err := st.GetUserAI(2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "gemini" || cfg.APIKey != "user-key" {
		t.Fatalf("user cfg stored: %+v", cfg)
	}

	admin := client(t)
	login(t, admin, ts.URL, "admin", "secret-admin")
	csrfAdmin := csrfToken(t, admin, ts.URL)
	resp, err = admin.PostForm(ts.URL+"/admin/usuarios/2/ia", url.Values{"csrf": {csrfAdmin}, "provider": {"openai"}, "api_key": {"forced-key"}, "force": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin force AI: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	cfg, err = st.GetEffectiveUserAI(2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" || cfg.APIKey != "forced-key" {
		t.Fatalf("effective cfg after force: %+v", cfg)
	}
	forced, err := st.IsUserAIFixed(2)
	if err != nil || !forced {
		t.Fatalf("force flag: %v %v", forced, err)
	}
}

func TestAIProviderKeyFormatValidation(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}

	if err := st.SetUserAI(2, "openai", "AIza...fake-gemini-key"); err == nil {
		t.Fatal("expected OpenAI key validation to reject Gemini key")
	}
	if err := st.SetUserAI(2, "gemini", "AIza...fake-gemini-key"); err != nil {
		t.Fatalf("expected Gemini key format to be accepted: %v", err)
	}
	if err := st.SetUserAI(2, "claude", "sk-ant-api03-foo"); err != nil {
		t.Fatalf("expected Claude key format to be accepted: %v", err)
	}
	if err := st.SetUserAI(2, "openai", "sk-proj-abc123"); err != nil {
		t.Fatalf("expected OpenAI key format to be accepted: %v", err)
	}
}

func TestAPIListProviderModels(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}
	st.SetUserAI(2, "gemini", "AIzaFakeKey1234567890")

	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "generativelanguage.googleapis.com" && req.URL.Path == "/v1beta/models" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[{"name":"models/gemini-2.0-flash"},{"name":"models/gemini-1.5-flash"}]}`))}, nil
		}
		return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.String())
	})}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	kid := client(t)
	login(t, kid, ts.URL, "nino", "1234")
	resp, err := kid.PostForm(ts.URL+"/api/ia/models", url.Values{"provider": {"gemini"}, "api_key": {"AIzaFakeKey1234567890"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("models: %d %s", resp.StatusCode, string(b))
	}
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if models, ok := v["models"].([]any); !ok || len(models) != 2 {
		t.Fatalf("unexpected models payload: %#v", v)
	}
}

func TestGeminiModelFallbackOnNotFound(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAI(2, "gemini", "AIzaFakeKey1234567890"); err != nil {
		t.Fatal(err)
	}

	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1beta/models/gemini-2.5-flash:generateContent":
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"model not found"}}`))}, nil
		case "/v1beta/models/gemini-2.0-flash:generateContent":
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"fallback ok"}]}}]}`))}, nil
		default:
			return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"unexpected"}`))}, nil
		}
	})}

	text, err := srv.callGemini("gemini-2.5-flash", "hola", "", "AIzaFakeKey1234567890", 0, nil, false)
	if err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	if text != "fallback ok" {
		t.Fatalf("unexpected fallback text: %q", text)
	}
}

func TestAPIAuthBearerToken(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAI(2, "gemini", "super-secret"); err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateSession(2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/ia/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("bearer auth status: %d body=%s", resp.StatusCode, string(b))
	}
}

func TestAPIv2Contract(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAdminPassword("secret-admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("nino", "1234", false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAI(2, "gemini", "super-secret"); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"candidates":[{"content":{"parts":[{"text":"respuesta OK"}]}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	anon := client(t)
	resp, err := anon.Get(ts.URL + "/api/ia/config")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("config should require auth: %d", resp.StatusCode)
	}
	var errBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode auth error: %v", err)
	}
	if errBody["code"] != "unauthenticated" || errBody["loginUrl"] != "/login" {
		t.Fatalf("unauth response: %#v", errBody)
	}
	resp.Body.Close()

	kid := client(t)
	login(t, kid, ts.URL, "nino", "1234")
	resp, err = kid.Get(ts.URL + "/api/ia/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config status: %d", resp.StatusCode)
	}
	var cfg map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg["defaultProvider"] != "gemini" || cfg["images"] != true {
		t.Fatalf("config incorrect: %#v", cfg)
	}

	resp, err = kid.Post(ts.URL+"/api/ia", "application/json", strings.NewReader(`{"prompt":"responde con una frase","images":[{"mimeType":"image/jpeg","data":"AQID"}],"json":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("ia call status: %d body=%s", resp.StatusCode, string(b))
	}
	var ia map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&ia); err != nil {
		t.Fatalf("decode IA response: %v", err)
	}
	if ia["text"] != "respuesta OK" {
		t.Fatalf("IA response: %#v", ia)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/ver/1": "/ver/1", "//evil.com": "/", "https://evil.com": "/", "/\\evil.com": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseTags(t *testing.T) {
	got := store.ParseTags(" sumas ,Sumas,  hora  del día ,, a|b")
	want := []string{"sumas", "hora del día", "a/b"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("got %q", got)
	}
}
