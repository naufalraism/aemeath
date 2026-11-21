package aemeath_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// Handlers read the storage as the aemeath.Storage interface. StoragePlugin must
// provide it under that type, not under the concrete type that was passed in.
func TestStoragePluginProvidesTheStorageInterface(t *testing.T) {
	store := aemeath.NewLocalStorage(t.TempDir())
	app := aemeath.New()
	if err := app.RegisterPlugin(&aemeath.StoragePlugin{Storage: store}); err != nil {
		t.Fatal(err)
	}

	called := false
	app.Handle("GET", "/s", func(c *aemeath.Ctx) error {
		called = true
		s, ok := aemeath.Get[aemeath.Storage](c)
		if !ok {
			t.Error("the storage is not available as aemeath.Storage")
		} else if s != aemeath.Storage(store) {
			t.Error("a different storage was provided")
		}
		if _, ok := aemeath.Get[*aemeath.LocalStorage](c); ok {
			t.Error("the storage must be provided as the interface only")
		}
		return nil
	})
	get(app, "/s")
	if !called {
		t.Fatal("the handler did not run")
	}
}

// A PNG header followed by <script>, uploaded as "avatar.html", passed
// accept:"image/png", kept its .html name and was served as text/html by
// app.Static: stored XSS.
func TestLocalStorageNeutralizesActiveExtensions(t *testing.T) {
	dir := t.TempDir()
	s := aemeath.NewLocalStorage(dir)
	app := aemeath.New()
	app.Static("/uploads", dir)

	for name, wantSuffix := range map[string]string{
		"avatar.html":  "_avatar.html.txt",
		"x.SVG":        "_x.SVG.txt",
		"a.js":         "_a.js.txt",
		"page.htm.":    "_page.htm.txt", // Windows would drop the trailing dot
		"shell.php  ":  "_shell.php.txt",
		"photo.png":    "_photo.png",
		"report.pdf":   "_report.pdf",
		"notes":        "_notes",
		"../evil.html": "_evil.html.txt",
	} {
		stored, err := s.Save(name, strings.NewReader("\x89PNG\r\n\x1a\n<script>alert(1)</script>"))
		if err != nil {
			t.Fatalf("Save(%q): %v", name, err)
		}
		if !strings.HasSuffix(stored, wantSuffix) {
			t.Errorf("Save(%q) = %q, want suffix %q", name, stored, wantSuffix)
			continue
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "/uploads/"+stored, nil))
		if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "html") || strings.Contains(ct, "svg") || strings.Contains(ct, "javascript") {
			t.Errorf("%q is served as %s", stored, ct)
		}
	}
}

// A colon and the other characters Windows refuses in a file name are
// replaced. On NTFS "x.html::$DATA" names the file x.html itself, so it
// slipped past the .html -> .html.txt rename and the upload was stored as
// an HTML page; "a.png:evil.html" wrote a hidden stream of a.png (B5 of the
// security audit). The stored name is now the same, and harmless, on every OS.
func TestSaveReplacesWindowsReservedCharacters(t *testing.T) {
	st := aemeath.NewLocalStorage(t.TempDir())
	for _, tc := range []struct{ in, suffix string }{
		{"x.html::$DATA", "_x.html__$DATA"},
		{"a.png:evil.html", "_a.png_evil.html.txt"},
		{"report?.pdf", "_report_.pdf"},
		{`a"b|c<d>e*.txt`, "_a_b_c_d_e_.txt"},
	} {
		name, err := st.Save(tc.in, strings.NewReader("<script>alert(1)</script>"))
		if err != nil {
			t.Fatalf("Save(%q): %v", tc.in, err)
		}
		if strings.ContainsAny(name, `<>:"|?*`) || !strings.HasSuffix(name, tc.suffix) {
			t.Errorf("Save(%q) = %q, want a name ending in %q", tc.in, name, tc.suffix)
		}
		if _, err := os.Stat(filepath.Join(st.UploadDir, name)); err != nil {
			t.Errorf("Save(%q): the returned name is not the file on disk: %v", tc.in, err)
		}
	}
}
