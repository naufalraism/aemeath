package aemeath

import (
	"net/http"
	"strings"
)

// Static mendaftarkan direktori untuk menyajikan file statis.
// Contoh: app.Static("/public", "./assets") akan menyajikan folder "./assets" di URL "/public/...".
func (a *App) Static(prefix string, dir string) {
	// Pastikan prefix diawali dan diakhiri dengan "/"
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix = prefix + "/"
	}

	// http.FileServer adalah handler bawaan Go untuk serve file
	fs := http.FileServer(http.Dir(dir))

	// Daftarkan langsung ke mux (melewati middleware JSON kita karena ini file statis)
	a.mux.Handle("GET "+prefix, http.StripPrefix(prefix, fs))

	// Redirect jika user mengakses tanpa trailing slash
	a.mux.Handle("GET "+strings.TrimSuffix(prefix, "/"), http.RedirectHandler(prefix, http.StatusMovedPermanently))
}
