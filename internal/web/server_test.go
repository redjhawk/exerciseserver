package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
