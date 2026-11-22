package aemeath_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/naufalraism/aemeath"
)

// A symbolic link inside the static folder must not serve files outside it
// (B2 of the security audit: public/link.txt -> ../secret.txt answered 200
// with the secret). Links that stay inside still work.
func TestStaticDoesNotFollowLinksOutside(t *testing.T) {
	root := t.TempDir()
	pub := filepath.Join(root, "public")
	must(t, os.MkdirAll(filepath.Join(pub, "img"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "private"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "secret.txt"), []byte("TOPSECRET"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "private", "key.txt"), []byte("TOPSECRET"), 0o644))
	must(t, os.WriteFile(filepath.Join(pub, "img", "logo.txt"), []byte("logo"), 0o644))
	links := map[string]string{
		filepath.Join(pub, "link.txt"):   filepath.Join(root, "secret.txt"), // file outside, absolute
		filepath.Join(pub, "rel.txt"):    filepath.Join("..", "secret.txt"), // file outside, relative
		filepath.Join(pub, "dir"):        filepath.Join(root, "private"),    // directory outside
		filepath.Join(pub, "inside.txt"): filepath.Join("img", "logo.txt"),  // file inside
		filepath.Join(root, "site"):      pub,                               // the folder itself is a link
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("cannot create symbolic links here (%v); on Windows enable Developer Mode", err)
		}
	}

	app := aemeath.New()
	app.Static("/s", pub)
	app.Static("/site", filepath.Join(root, "site"))
	app.Static("/later", filepath.Join(root, "later")) // does not exist yet

	must(t, os.MkdirAll(filepath.Join(root, "later"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "later", "a.txt"), []byte("later"), 0o644))

	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/s/link.txt", 404, ""},
		{"/s/rel.txt", 404, ""},
		{"/s/dir/key.txt", 404, ""},
		{"/s/../secret.txt", 404, ""},
		{"/s/img/logo.txt", 200, "logo"},
		{"/s/inside.txt", 200, "logo"},
		{"/site/img/logo.txt", 200, "logo"},
		{"/later/a.txt", 200, "later"},
	} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.code || (tc.body != "" && w.Body.String() != tc.body) || (tc.code != 200 && w.Body.String() == "TOPSECRET") {
			t.Errorf("GET %s: %d %q, want %d %q", tc.path, w.Code, w.Body.String(), tc.code, tc.body)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
