package securityaudit

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/storage"
)

// A PNG header followed by HTML: http.DetectContentType sees image/png.
var polyglot = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), []byte(strings.Repeat(" ", 600)+"<script>alert(document.cookie)</script>")...)

func multipartBody(t *testing.T, field, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	_ = w.Close()
	return &buf, w.FormDataContentType()
}

// Upload with `accept:"image/png"`, stored with storage.Local, served with
// app.Static: the documented way to build an avatar upload.
func TestUpload_PolyglotServedAsHTML(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	app := aemeath.New()
	app.Use(aemeath.SecurityHeaders())
	app.Static("/uploads", dir)

	type avatarIn struct {
		File *multipart.FileHeader `form:"avatar" validate:"required" accept:"image/png" maxsize:"1MB"`
	}
	var stored string
	aemeath.POST(app, "/avatar", func(c *aemeath.Ctx, in avatarIn) (map[string]string, error) {
		f, err := in.File.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		stored, err = store.Save(c.Context(), in.File.Filename, f)
		return map[string]string{"url": "/uploads/" + stored}, err
	})

	body, ct := multipartBody(t, "avatar", "avatar.html", polyglot)
	r := httptest.NewRequest("POST", "/avatar", body)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	t.Logf("upload: %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	if w.Code != 200 {
		t.Logf("upload rejected: defence works")
		return
	}

	w = httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/uploads/"+stored, nil))
	got := w.Header().Get("Content-Type")
	t.Logf("served %s as Content-Type %q, Content-Disposition %q", stored, got, w.Header().Get("Content-Disposition"))
	b, _ := io.ReadAll(w.Body)
	if strings.HasPrefix(got, "text/html") && bytes.Contains(b, []byte("<script>")) {
		t.Errorf("WEAKNESS: a file accepted as image/png is stored as %q and served as %s: stored XSS on this origin", stored, got)
	}
}

// Content type is sniffed from the first 512 bytes only.
func TestUpload_AcceptChecksOnlyMagicBytes(t *testing.T) {
	app := aemeath.New()
	type in struct {
		File *multipart.FileHeader `form:"f" validate:"required" accept:"image/png"`
	}
	aemeath.POST(app, "/u", func(c *aemeath.Ctx, _ in) (string, error) { return "accepted", nil })
	body, ct := multipartBody(t, "f", "x.png", polyglot)
	r := httptest.NewRequest("POST", "/u", body)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	t.Logf("polyglot with accept=image/png -> %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	if w.Code == http.StatusOK {
		t.Log("NOTE: accept only checks magic bytes (by design); the file still contains HTML/JS after byte 512")
	}
}

// The client's file name must never escape the upload directory.
func TestUpload_FilenameTraversal(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	for _, name := range []string{"../../etc/passwd", `..\..\win.ini`, "/abs/path.txt", "..", ".", "", "a/../../b.txt"} {
		stored, err := store.Save(t.Context(), name, strings.NewReader("x"))
		if err != nil {
			t.Errorf("Save(%q): %v", name, err)
			continue
		}
		if strings.ContainsAny(stored, `/\`) || strings.Contains(stored, "..") {
			t.Errorf("WEAKNESS: Save(%q) stored as %q", name, stored)
		}
		t.Logf("Save(%q) -> %q", name, stored)
	}
}
