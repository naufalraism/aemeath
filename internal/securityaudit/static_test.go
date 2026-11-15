package securityaudit

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestStatic_TraversalAndHiddenFiles(t *testing.T) {
	root := t.TempDir()
	pub := filepath.Join(root, "public")
	_ = os.MkdirAll(pub, 0o755)
	_ = os.WriteFile(filepath.Join(root, "secret.txt"), []byte("TOPSECRET"), 0o644)
	_ = os.WriteFile(filepath.Join(pub, ".env"), []byte("TOPSECRET"), 0o644)
	_ = os.WriteFile(filepath.Join(pub, "ok.txt"), []byte("public"), 0o644)
	app := aemeath.New()
	app.Static("/s", pub)
	for _, p := range []string{
		"/s/../secret.txt", "/s/%2e%2e/secret.txt", "/s/..%2fsecret.txt", "/s/%2e%2e%2fsecret.txt",
		"/s/....//secret.txt", "/s/.env", "/s/%2eenv", "/s/ok.txt/../../secret.txt", "/s//../secret.txt",
	} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		leaked := strings.Contains(w.Body.String(), "TOPSECRET")
		t.Logf("%-30s -> %d leaked=%v", p, w.Code, leaked)
		if leaked {
			t.Errorf("WEAKNESS: %s leaked a file", p)
		}
	}
}

// http.Dir follows symbolic links. Only a problem if someone can create one
// inside the served directory (an extracted zip, a shared volume).
func TestStatic_FollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	pub := filepath.Join(root, "public")
	_ = os.MkdirAll(pub, 0o755)
	_ = os.WriteFile(filepath.Join(root, "secret.txt"), []byte("TOPSECRET"), 0o644)
	if err := os.Symlink(filepath.Join(root, "secret.txt"), filepath.Join(pub, "link.txt")); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	app := aemeath.New()
	app.Static("/s", pub)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/s/link.txt", nil))
	t.Logf("/s/link.txt (symlink to a file outside the folder) -> %d %q", w.Code, w.Body.String())
	if strings.Contains(w.Body.String(), "TOPSECRET") {
		t.Log("NOTE: symlinks inside the served folder are followed outside it")
	}
}
