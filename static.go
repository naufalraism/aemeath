package aemeath

import (
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// rootDir serves the files of a directory without ever leaving it. Unlike
// http.Dir, which follows a symbolic link wherever it points, each file is
// opened with os.OpenInRoot: a link inside dir that resolves outside it
// (public/link.txt -> ../secret.txt, or to /etc) is not found, while links
// that stay inside dir still work. dir itself may be a link, and need not
// exist when Static is called.
type rootDir string

func (d rootDir) Open(name string) (http.File, error) {
	if strings.ContainsRune(name, 0) {
		return nil, fs.ErrNotExist
	}
	rel := strings.TrimPrefix(path.Clean("/"+name), "/")
	if rel == "" {
		rel = "."
	}
	f, err := os.OpenInRoot(string(d), filepath.FromSlash(rel))
	if err != nil {
		// Outside the root, or not there: both are a plain 404.
		return nil, fs.ErrNotExist
	}
	return f, nil
}

// noListingFS hides directory listings: a directory is only served when it
// contains an index.html, otherwise it is reported as not found. It also
// hides dotfiles and dot-directories (.env, .git, .htpasswd, ...), except
// .well-known, which exists to be public.
type noListingFS struct{ fs http.FileSystem }

// hidden reports whether a path contains a segment that starts with a dot.
func hidden(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if seg != "" && seg != "." && seg != ".well-known" && strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func (n noListingFS) Open(name string) (http.File, error) {
	if hidden(name) {
		return nil, fs.ErrNotExist
	}
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if st.IsDir() {
		idx, err := n.fs.Open(path.Join(name, "index.html"))
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		_ = idx.Close()
	}
	return f, nil
}

// noSniff stops browsers and the file server from guessing a file's type from
// its content. Without it, a file whose name has no extension, or one this
// system has no type for ("note", "report.dat"), is sniffed, and an uploaded
// file that starts with "<!doctype html>" is served as text/html: stored XSS
// on the site's own origin. Such files are sent as application/octet-stream
// instead, which browsers download rather than display.
func noSniff(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if p := r.URL.Path; p != "" && !strings.HasSuffix(p, "/") && !knownType(path.Ext(p)) {
			// A directory asked for without its slash only gets a redirect.
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		h.ServeHTTP(w, r)
	})
}

func knownType(ext string) bool { return ext != "" && mime.TypeByExtension(ext) != "" }

func httpHandler(h http.Handler) HandlerFunc {
	return func(c *Ctx) error {
		h.ServeHTTP(c.W, c.R)
		return nil
	}
}

// Static serves the files in dir under the URL prefix. Directory listings are
// disabled, dotfiles (.env, .git/...) are never served, and global middlewares (Logger, CORS, SecurityHeaders, ...) apply.
// Every file is sent with X-Content-Type-Options: nosniff, and a file whose
// extension has no known type is sent as application/octet-stream rather
// than guessed from its content, so an uploaded file can never run as a page.
func (a *App) Static(prefix string, dir string) {
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	fs := noSniff(http.FileServer(noListingFS{rootDir(dir)}))

	// A root prefix is registered directly, without a redirect.
	if prefix == "/" {
		a.Handle("GET", "/", httpHandler(fs))
		return
	}

	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	a.Handle("GET", prefix, httpHandler(http.StripPrefix(prefix, fs)))

	// Redirect when the user omits the trailing slash.
	a.Handle("GET", strings.TrimSuffix(prefix, "/"), httpHandler(http.RedirectHandler(prefix, http.StatusMovedPermanently)))
}
