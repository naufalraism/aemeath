package aemeath_test

import (
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// FuzzRealIP sends arbitrary X-Forwarded-For and X-Real-IP headers from a
// trusted proxy and from an untrusted client. Whatever the headers say:
//   - an untrusted peer's address is never replaced (no spoofing);
//   - the address the handler sees is always a valid host:port;
//   - a replaced address was taken from one of the headers;
//   - when the header ends with a valid untrusted address followed only by
//     trusted proxies, that address is the one chosen.
func FuzzRealIP(f *testing.F) {
	for _, s := range []string{
		"203.0.113.7", "203.0.113.7, 10.0.0.2", "1.1.1.1, 2.2.2.2, 10.0.0.9", "garbage, 203.0.113.7",
		"203.0.113.7, garbage", "", " , ,", "::ffff:203.0.113.7", "2001:db8::1", "fe80::1%eth0",
		"10.0.0.1, 10.0.0.2", "[2001:db8::1]:443", "203.0.113.7:80", "1.2.3.4\x00", strings.Repeat("1.1.1.1,", 200),
	} {
		f.Add(s, "198.51.100.4")
	}
	trusted := []string{"10.0.0.0/8", "127.0.0.1"}
	isTrusted := func(a netip.Addr) bool {
		a = a.Unmap()
		return netip.MustParsePrefix("10.0.0.0/8").Contains(a) || a == netip.MustParseAddr("127.0.0.1")
	}
	app := aemeath.New()
	app.Use(aemeath.RealIP(trusted...))
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { _, err := io.WriteString(c.W, c.R.RemoteAddr); return err })

	f.Fuzz(func(t *testing.T, xff, realIP string) {
		for _, peer := range []string{"10.0.0.5:4000", "198.51.100.9:4000"} {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = peer
			r.Header["X-Forwarded-For"] = []string{xff}
			r.Header["X-Real-Ip"] = []string{realIP}
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			got := w.Body.String()

			if peer == "198.51.100.9:4000" {
				if got != peer {
					t.Fatalf("untrusted peer: RemoteAddr became %q (X-Forwarded-For %q, X-Real-IP %q)", got, xff, realIP)
				}
				continue
			}
			host, _, err := net.SplitHostPort(got)
			if err != nil {
				t.Fatalf("RemoteAddr %q is not host:port: %v", got, err)
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				t.Fatalf("RemoteAddr host %q is not an IP: %v", host, err)
			}
			if got == peer {
				continue
			}
			if !strings.Contains(xff, strings.Split(addr.String(), "%")[0]) && !strings.Contains(realIP, strings.Split(addr.String(), "%")[0]) &&
				!containsUnmapped(xff+","+realIP, addr) {
				t.Fatalf("RemoteAddr %q comes from neither header (X-Forwarded-For %q, X-Real-IP %q)", got, xff, realIP)
			}
			// The right-most valid, untrusted hop with only valid trusted hops after it must win.
			hops := strings.Split(xff, ",")
			for i := len(hops) - 1; i >= 0; i-- {
				a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
				if err != nil {
					break
				}
				if !isTrusted(a) {
					if a.Unmap() != addr {
						t.Fatalf("X-Forwarded-For %q: chose %v, want the right-most untrusted hop %v", xff, addr, a.Unmap())
					}
					break
				}
			}
		}
	})
}

func containsUnmapped(s string, addr netip.Addr) bool {
	for _, part := range strings.Split(s, ",") {
		if a, err := netip.ParseAddr(strings.TrimSpace(part)); err == nil && a.Unmap() == addr {
			return true
		}
	}
	return false
}

// FuzzStaticPaths requests arbitrary paths under a Static prefix. Whatever
// the path, a file outside the served folder or a dotfile is never sent, and
// the server never answers 500.
func FuzzStaticPaths(f *testing.F) {
	for _, s := range []string{
		"index.html", "../secret.txt", "..%2fsecret.txt", "%2e%2e/secret.txt", "....//secret.txt", `..\secret.txt`,
		"a/../../secret.txt", "/../../secret.txt", ".env", ".git/config", "sub/.env", "%2e%2e%2fsecret.txt",
		"sub/../../secret.txt", "./././index.html", "sub//x.txt", "\x00", "C:\\secret.txt", "..;/secret.txt",
	} {
		f.Add(s)
	}
	base := f.TempDir()
	root := filepath.Join(base, "public")
	must(f, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(f, os.WriteFile(filepath.Join(base, "secret.txt"), []byte("TOPSECRET-OUTSIDE"), 0o644))
	must(f, os.WriteFile(filepath.Join(root, ".env"), []byte("TOPSECRET-DOTFILE"), 0o644))
	must(f, os.WriteFile(filepath.Join(root, "sub", ".env"), []byte("TOPSECRET-DOTFILE"), 0o644))
	must(f, os.WriteFile(filepath.Join(root, "index.html"), []byte("<p>hi</p>"), 0o644))
	must(f, os.WriteFile(filepath.Join(root, "sub", "x.txt"), []byte("x"), 0o644))

	app := aemeath.New()
	app.Static("/s", root)

	f.Fuzz(func(t *testing.T, p string) {
		r := httptest.NewRequest("GET", "/", nil)
		r.URL.Path = "/s/" + p
		r.URL.RawPath = ""
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code >= 500 {
			t.Fatalf("GET /s/%q -> %d", p, w.Code)
		}
		if strings.Contains(w.Body.String(), "TOPSECRET") {
			t.Fatalf("GET /s/%q -> %d served a file it must not: %q", p, w.Code, w.Body.String())
		}
	})
}
