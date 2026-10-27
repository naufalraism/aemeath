package aemeath

import (
	"net/http"
	"path"
	"strings"
)

// noListingFS hides directory listings: a directory is only served when it
// contains an index.html, otherwise it is reported as not found.
type noListingFS struct{ fs http.FileSystem }

func (n noListingFS) Open(name string) (http.File, error) {
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

func httpHandler(h http.Handler) HandlerFunc {
	return func(c *Ctx) error {
		h.ServeHTTP(c.W, c.R)
		return nil
	}
}

// Static serves the files in dir under the URL prefix. Directory listings are
// disabled, and global middlewares (Logger, CORS, SecurityHeaders, ...) apply.
func (a *App) Static(prefix string, dir string) {
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	fs := http.FileServer(noListingFS{http.Dir(dir)})

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
