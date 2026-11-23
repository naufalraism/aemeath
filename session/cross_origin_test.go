package session_test

import (
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

type originCase struct {
	name, method, origin, site string
	want                       int
}

func runOriginCases(t *testing.T, cfg session.Config, cases []originCase) {
	t.Helper()
	if cfg.Store == nil {
		cfg.Store = session.NewMemoryStore()
	}
	app := aemeath.New()
	app.Use(session.Middleware(cfg))
	handler := func(c *aemeath.Ctx) error { return c.Text(200, "done") }
	app.Handle("POST", "/transfer", handler)
	app.Handle("GET", "/transfer", handler)
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, "https://shop.example/transfer", nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if tc.site != "" {
			r.Header.Set("Sec-Fetch-Site", tc.site)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: %s with Origin %q, Sec-Fetch-Site %q -> %d %s, want %d", tc.name, tc.method, tc.origin, tc.site, w.Code, w.Body.String(), tc.want)
		}
	}
}

// Cookies are sent with requests a hostile page triggers. With only
// session.Middleware installed (no csrf.Middleware), a POST from another
// site was served. The session middleware now refuses cross-origin writes
// the way net/http.CrossOriginProtection does: by Sec-Fetch-Site, or by
// Origin on browsers that do not send it. Same-origin pages, reads, and
// clients that are not browsers (curl, mobile apps, webhooks) are unchanged.
func TestSessionRefusesCrossOriginWrites(t *testing.T) {
	runOriginCases(t, session.Config{}, []originCase{
		{"own page", "POST", "https://shop.example", "same-origin", 200},
		{"typed in the address bar", "POST", "", "none", 200},
		{"hostile site", "POST", "https://evil.example", "cross-site", 403},
		{"older browser, Origin only", "POST", "https://evil.example", "", 403},
		{"sibling subdomain", "POST", "https://app.shop.example", "same-site", 403},
		{"curl or a mobile app", "POST", "", "", 200},
		{"a read from another site", "GET", "https://evil.example", "cross-site", 200},
	})
}

func TestSessionTrustedOrigins(t *testing.T) {
	runOriginCases(t, session.Config{TrustedOrigins: []string{"https://app.shop.example"}}, []originCase{
		{"trusted subdomain", "POST", "https://app.shop.example", "same-site", 200},
		{"trusted, older browser", "POST", "https://app.shop.example", "", 200},
		{"another site", "POST", "https://evil.example", "cross-site", 403},
	})
}

func TestSessionAllowCrossOrigin(t *testing.T) {
	runOriginCases(t, session.Config{AllowCrossOrigin: true}, []originCase{
		{"check turned off", "POST", "https://evil.example", "cross-site", 200},
	})
}

func TestSessionRefusesAnInvalidTrustedOrigin(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error(`TrustedOrigins: "app.shop.example" (no scheme) did not panic at startup`)
		}
	}()
	session.Middleware(session.Config{Store: session.NewMemoryStore(), TrustedOrigins: []string{"app.shop.example"}})
}
