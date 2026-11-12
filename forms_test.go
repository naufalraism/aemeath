package aemeath_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type UploadIn struct {
	Title  string                  `form:"title" validate:"required,min=3"`
	Tags   []string                `form:"tags"`
	Age    *int                    `form:"age" validate:"min=1"`
	Avatar *multipart.FileHeader   `form:"avatar" validate:"required" accept:"image/png,image/jpeg" maxsize:"1KB"`
	Docs   []*multipart.FileHeader `form:"docs" accept:"text/*"`
}

type UploadOut struct {
	Title  string   `json:"title"`
	Tags   []string `json:"tags"`
	Size   int64    `json:"size"`
	Docs   int      `json:"docs"`
	Secret string   `json:"secret"`
}

func formApp(t *testing.T) *aemeath.App {
	app := aemeath.New()
	aemeath.POST(app, "/upload", func(c *aemeath.Ctx, in UploadIn) (UploadOut, error) {
		f, err := in.Avatar.Open() // must still be readable inside the handler
		if err != nil {
			return UploadOut{}, err
		}
		defer func() { _ = f.Close() }()
		b, _ := io.ReadAll(f)
		return UploadOut{Title: in.Title, Tags: in.Tags, Size: int64(len(b)), Docs: len(in.Docs)}, nil
	})
	return app
}

// 1x1 PNG
var png = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}

type part struct {
	name, filename string
	data           []byte
}

func multipartReq(t *testing.T, fields url.Values, files []part) *http.Request {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, vs := range fields {
		for _, v := range vs {
			_ = w.WriteField(k, v)
		}
	}
	for _, f := range files {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="`+f.name+`"; filename="`+f.filename+`"`)
		h.Set("Content-Type", "application/x-lies") // the client's claim must be ignored
		p, _ := w.CreatePart(h)
		_, _ = p.Write(f.data)
	}
	_ = w.Close()
	r := httptest.NewRequest("POST", "/upload", &buf)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

func serve(app *aemeath.App, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestMultipartBindsFieldsSlicesAndFiles(t *testing.T) {
	app := formApp(t)
	r := multipartReq(t, url.Values{"title": {"Hello"}, "tags": {"a", "b"}, "age": {"7"}},
		[]part{{"avatar", "me.png", png}, {"docs", "a.txt", []byte("hello")}, {"docs", "b.txt", []byte("world")}})
	w := serve(app, r)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out UploadOut
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Title != "Hello" || len(out.Tags) != 2 || out.Size != int64(len(png)) || out.Docs != 2 {
		t.Fatalf("%+v", out)
	}
}

func fieldErrors(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	if w.Code != 422 {
		t.Fatalf("want 422, got %d %s", w.Code, w.Body)
	}
	var e struct {
		Errors []aemeath.FieldError `json:"errors"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	m := map[string]string{}
	for _, fe := range e.Errors {
		m[fe.Field] = fe.Rule
	}
	return m
}

func TestFormValidationAndFileRules(t *testing.T) {
	app := formApp(t)
	// missing required file + short title + invalid optional number
	m := fieldErrors(t, serve(app, multipartReq(t, url.Values{"title": {"x"}, "age": {"0"}}, nil)))
	if m["title"] != "min" || m["avatar"] != "required" || m["age"] != "min" {
		t.Fatalf("%v", m)
	}
	// a text file renamed .png: the real content decides, not the name or client type
	m = fieldErrors(t, serve(app, multipartReq(t, url.Values{"title": {"Hello"}}, []part{{"avatar", "evil.png", []byte("<?php echo 1;")}})))
	if m["avatar"] != "accept" {
		t.Fatalf("%v", m)
	}
	// too large
	big := append(append([]byte{}, png...), bytes.Repeat([]byte{0}, 2048)...)
	m = fieldErrors(t, serve(app, multipartReq(t, url.Values{"title": {"Hello"}}, []part{{"avatar", "big.png", big}})))
	if m["avatar"] != "maxsize" {
		t.Fatalf("%v", m)
	}
	// two files in a single-file field
	m = fieldErrors(t, serve(app, multipartReq(t, url.Values{"title": {"Hello"}}, []part{{"avatar", "a.png", png}, {"avatar", "b.png", png}})))
	if m["avatar"] != "single" {
		t.Fatalf("%v", m)
	}
	// the docs slice enforces accept on every file
	m = fieldErrors(t, serve(app, multipartReq(t, url.Values{"title": {"Hello"}}, []part{{"avatar", "a.png", png}, {"docs", "x.bin", []byte{0, 1, 2, 3}}})))
	if m["docs"] != "accept" {
		t.Fatalf("%v", m)
	}
}

func TestFormRejectsWrongContentTypeAndOversize(t *testing.T) {
	app := formApp(t)
	r := httptest.NewRequest("POST", "/upload", strings.NewReader(`{"title":"Hello"}`))
	r.Header.Set("Content-Type", "application/json")
	if w := serve(app, r); w.Code != 415 {
		t.Fatalf("json to form route: %d", w.Code)
	}
	r = httptest.NewRequest("POST", "/upload", strings.NewReader("title=Hello"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := serve(app, r); w.Code != 415 { // file field needs multipart
		t.Fatalf("urlencoded to file route: %d", w.Code)
	}
	old := aemeath.DefaultMaxMultipartBytes
	aemeath.DefaultMaxMultipartBytes = 1024
	defer func() { aemeath.DefaultMaxMultipartBytes = old }()
	w := serve(app, multipartReq(t, url.Values{"title": {"Hello"}}, []part{{"avatar", "a.png", bytes.Repeat(png, 100)}}))
	if w.Code != 413 {
		t.Fatalf("oversize: %d %s", w.Code, w.Body)
	}
}

type LoginForm struct {
	User string `form:"user" validate:"required"`
	Pass string `form:"pass" validate:"required"`
	Next string `query:"next"`
}

func TestUrlencodedFormIgnoresURLQueryForFormFields(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/login", func(c *aemeath.Ctx, in LoginForm) (map[string]string, error) {
		return map[string]string{"user": in.User, "next": in.Next}, nil
	})
	r := httptest.NewRequest("POST", "/login?next=/home&user=attacker", strings.NewReader("user=ann&pass=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := serve(app, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"user":"ann"`) || !strings.Contains(w.Body.String(), `"next":"/home"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestFormMisuseFailsAtStartup(t *testing.T) {
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: expected a startup panic", name)
			}
		}()
		f()
	}
	type onGet struct {
		A string `form:"a"`
	}
	mustPanic("form on GET", func() {
		aemeath.GET(aemeath.New(), "/x", func(*aemeath.Ctx, onGet) (aemeath.Handled, error) { return aemeath.Handled{}, nil })
	})
	type bothTags struct {
		A string `form:"a" query:"a"`
	}
	mustPanic("form+query", func() {
		aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, bothTags) (aemeath.Handled, error) { return aemeath.Handled{}, nil })
	})
	type badSize struct {
		F *multipart.FileHeader `form:"f" maxsize:"lots"`
	}
	mustPanic("bad maxsize", func() {
		aemeath.POST(aemeath.New(), "/x", func(*aemeath.Ctx, badSize) (aemeath.Handled, error) { return aemeath.Handled{}, nil })
	})
}

func TestFormGenerators(t *testing.T) {
	app := formApp(t)
	oa := app.OpenAPI(aemeath.Info{Title: "t", Version: "1"})
	b, _ := json.Marshal(oa)
	spec := string(b)
	for _, want := range []string{`"multipart/form-data"`, `"format":"binary"`, `"avatar"`, `"x-accept"`} {
		if !strings.Contains(spec, want) {
			t.Errorf("openapi missing %s\n%s", want, spec)
		}
	}
	if strings.Contains(spec, `"application/json"`) && strings.Contains(spec, `"requestBody":{"content":{"application/json"`) {
		t.Errorf("form route must not document a JSON body")
	}
	ts := app.TypeScriptClient()
	for _, want := range []string{"input: { form: { ", "title: string", "avatar: File | Blob", "docs?: (File | Blob)[]", "new FormData()"} {
		if !strings.Contains(ts, want) {
			t.Errorf("ts missing %q\n%s", want, ts)
		}
	}
	z := app.ZodSchemas()
	if !strings.Contains(z, "z.instanceof(Blob)") || !strings.Contains(z, "FormSchema") {
		t.Errorf("zod:\n%s", z)
	}
}

func TestFormBodyValuesNeverComeFromTheURL(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/login", func(c *aemeath.Ctx, in LoginForm) (map[string]string, error) {
		return map[string]string{"user": in.User}, nil
	})
	// `user` only in the URL, not in the body: must count as missing.
	r := httptest.NewRequest("POST", "/login?user=attacker", strings.NewReader("pass=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := serve(app, r)
	if w.Code != 422 || strings.Contains(w.Body.String(), "attacker") {
		t.Fatalf("URL query leaked into a form field: %d %s", w.Code, w.Body)
	}
}

func TestLargeUploadsAreCleanedUpAfterTheRequest(t *testing.T) {
	dir := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, dir)
	}
	app := formApp(t)
	big := append(append([]byte{}, png...), bytes.Repeat([]byte{0}, 9<<20)...) // > the 8 MiB memory budget: spills to disk
	r := multipartReq(t, url.Values{"title": {"Hello"}}, []part{{"avatar", "big.png", big}})
	_ = serve(app, r) // 422 (too large for maxsize) is fine; files must still be removed
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("temporary upload files left behind: %d", len(entries))
	}
}
