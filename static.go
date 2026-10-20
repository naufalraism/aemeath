package aemeath

import (
	"net/http"
	"strings"
)

// Static mendaftarkan direktori untuk menyajikan file statis.
func (a *App) Static(prefix string, dir string) {
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}

	// FIX: Jika prefix adalah root "/", kita langsung daftarkan tanpa redirect
	if prefix == "/" {
		a.mux.Handle("GET /", http.FileServer(http.Dir(dir)))
		return
	}

	if !strings.HasSuffix(prefix, "/") {
		prefix = prefix + "/"
	}

	fs := http.FileServer(http.Dir(dir))
	a.mux.Handle("GET "+prefix, http.StripPrefix(prefix, fs))

	// Redirect jika user mengakses tanpa trailing slash
	a.mux.Handle("GET "+strings.TrimSuffix(prefix, "/"), http.RedirectHandler(prefix, http.StatusMovedPermanently))
}
