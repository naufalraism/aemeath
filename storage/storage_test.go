package storage_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/storage"
)

// The tests LocalStorage had in the aemeath package, on storage.Local, which
// now does the work.

func TestNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewLocal(dir)
	a, err := s.Save("avatar.png", strings.NewReader("USER-A"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save("avatar.png", strings.NewReader("USER-B"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two uploads got the same stored name")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, a)); string(data) != "USER-A" {
		t.Fatalf("first upload was overwritten: %q", data)
	}
}

func TestStaysInsideUploadDir(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewLocal(filepath.Join(dir, "uploads"))
	for _, name := range []string{"../../evil.txt", `..\..\evil.txt`, "/etc/passwd", "..", ""} {
		stored, err := s.Save(name, strings.NewReader("x"))
		if err != nil {
			t.Fatalf("Save(%q): %v", name, err)
		}
		if strings.ContainsAny(stored, `/\`) {
			t.Fatalf("Save(%q) returned a path: %q", name, stored)
		}
		if _, err := os.Stat(filepath.Join(dir, "uploads", stored)); err != nil {
			t.Fatalf("Save(%q): file not inside upload dir: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("file escaped the upload directory")
	}
	// Delete strips directories the same way.
	outside := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("../keep.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("Delete removed a file outside the upload directory")
	}
}

func TestFilesArePrivateByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	dir := filepath.Join(t.TempDir(), "uploads")
	s := storage.NewLocal(dir)
	name, err := s.Save("a.txt", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dir, name)); info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", info.Mode().Perm())
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 {
		t.Fatalf("directory mode = %o, want 750", info.Mode().Perm())
	}
	open := &storage.Local{UploadDir: filepath.Join(t.TempDir(), "pub"), FileMode: 0o644, DirMode: 0o755}
	name, err = open.Save("b.txt", strings.NewReader("y"))
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(open.UploadDir, name)); info.Mode().Perm() != 0o644 {
		t.Fatalf("configured file mode = %o, want 644", info.Mode().Perm())
	}
}

func TestNeutralizesActiveExtensions(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewLocal(dir)
	app := aemeath.New()
	app.Static("/uploads", dir)
	for name, wantSuffix := range map[string]string{
		"avatar.html":  "_avatar.html.txt",
		"x.SVG":        "_x.SVG.txt",
		"a.js":         "_a.js.txt",
		"page.htm.":    "_page.htm.txt",
		"shell.php  ":  "_shell.php.txt",
		"photo.png":    "_photo.png",
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

func TestReplacesWindowsReservedCharacters(t *testing.T) {
	st := storage.NewLocal(t.TempDir())
	for _, tc := range []struct{ in, suffix string }{
		{"x.html::$DATA", "_x.html__$DATA"},
		{"a.png:evil.html", "_a.png_evil.html.txt"},
		{"report?.pdf", "_report_.pdf"},
		{`a"b|c<d>e*.txt`, "_a_b_c_d_e_.txt"},
		{"evil\u202egnp.exe", "_evil_gnp.exe"},
		{"line\nbreak.txt", "_line_break.txt"},
	} {
		name, err := st.Save(tc.in, strings.NewReader("x"))
		if err != nil {
			t.Fatalf("Save(%q): %v", tc.in, err)
		}
		if strings.ContainsAny(name, "<>:\"|?*\n\u202e") || !strings.HasSuffix(name, tc.suffix) {
			t.Errorf("Save(%q) = %q, want a name ending in %q", tc.in, name, tc.suffix)
		}
	}
}

func TestDeleteOfAMissingFileIsNotAnError(t *testing.T) {
	if err := storage.NewLocal(t.TempDir()).Delete("never-saved.txt"); err != nil {
		t.Fatal(err)
	}
}
