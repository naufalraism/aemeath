package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]*http.Cookie
	token   string
}

func (b *browser) do(method, path, ctype string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	for _, c := range b.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	return w
}

func (b *browser) csrf() {
	w := b.do("GET", "/api/csrf", "", nil, nil)
	var o struct {
		T string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &o)
	b.token = o.T
}

func (b *browser) post(path string, v any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(v)
	return b.do("POST", path, "application/json", raw, map[string]string{"X-CSRF-Token": b.token})
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	return &browser{t: t, h: h, cookies: map[string]*http.Cookie{}}
}

var png = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}

func upload(b *browser, title string, file []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", title)
	if file != nil {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="attachment"; filename="a.png"`)
		p, _ := mw.CreatePart(h)
		_, _ = p.Write(file)
	}
	_ = mw.Close()
	return b.do("POST", "/api/notes", mw.FormDataContentType(), buf.Bytes(), map[string]string{"X-CSRF-Token": b.token})
}

func TestTutorialFlow(t *testing.T) {
	app := newApp(true)
	ann := newBrowser(t, app)
	ann.csrf()

	if w := ann.post("/api/notes", map[string]string{"title": "x"}); w.Code != 401 && w.Code != 415 {
		t.Fatalf("anonymous create: %d", w.Code) // 415: the route takes a form, 401 once the form is right
	}
	if w := ann.post("/api/signup", map[string]string{"name": "ann", "password": "short"}); w.Code != 422 {
		t.Fatalf("weak password must be a 422: %d %s", w.Code, w.Body)
	}
	if w := ann.post("/api/signup", map[string]string{"name": "ann", "password": "long-enough"}); w.Code != 201 {
		t.Fatalf("signup: %d %s", w.Code, w.Body)
	}
	ann.csrf() // the session id changed at login, so fetch the token again

	w := upload(ann, "Hello", png)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"attachment":"`) {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := upload(ann, "Bad", []byte("<script>alert(1)</script>")); w.Code != 422 {
		t.Fatalf("non-image attachment: %d", w.Code)
	}
	if w := ann.do("GET", "/api/notes/1", "", nil, nil); w.Code != 200 {
		t.Fatalf("read own note: %d", w.Code)
	}

	bob := newBrowser(t, app)
	bob.csrf()
	bob.post("/api/signup", map[string]string{"name": "bob", "password": "long-enough"})
	bob.csrf()
	if w := bob.do("GET", "/api/notes/1", "", nil, nil); w.Code != 404 {
		t.Fatalf("another user's note must look missing: %d", w.Code)
	}
}

func TestCSRFProtectsStateChanges(t *testing.T) {
	b := newBrowser(t, newApp(true))
	b.csrf()
	b.post("/api/signup", map[string]string{"name": "ann", "password": "long-enough"})
	b.token = "" // an attacker's page cannot read the token
	if w := b.post("/api/logout", map[string]string{}); w.Code != 403 {
		t.Fatalf("forged logout accepted: %d", w.Code)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	b := newBrowser(t, newApp(true))
	b.csrf()
	b.post("/api/signup", map[string]string{"name": "ann", "password": "long-enough"})
	b.csrf()
	if w := b.post("/api/logout", map[string]string{}); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	b.csrf()
	if w := b.do("GET", "/api/notes/1", "", nil, nil); w.Code != 401 {
		t.Fatalf("after logout: %d", w.Code)
	}
}

func TestMetricsAndHealth(t *testing.T) {
	b := newBrowser(t, newApp(true))
	b.do("GET", "/healthz", "", nil, nil)
	out := b.do("GET", "/metrics", "", nil, nil).Body.String()
	if !strings.Contains(out, `route="/healthz"`) {
		t.Fatalf("metrics missing:\n%s", out)
	}
}

func TestSecureCookiesByDefault(t *testing.T) {
	b := newBrowser(t, newApp(false))
	w := b.do("GET", "/api/csrf", "", nil, nil)
	for _, c := range w.Result().Cookies() {
		if !c.Secure || !c.HttpOnly {
			t.Fatalf("cookie %s must be Secure+HttpOnly by default", c.Name)
		}
	}
}
