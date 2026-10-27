package oauth2_test

import (
	"net/http/httptest"
	"testing"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/oauth2"
)

func TestGenerateStateIsRandomAndURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		s, err := oauth2.GenerateState()
		if err != nil {
			t.Fatal(err)
		}
		if len(s) < 30 || seen[s] {
			t.Fatalf("state %q is too short or repeated", s)
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				t.Fatalf("state %q is not URL-safe", s)
			}
		}
		seen[s] = true
	}
}

func TestSeveralProvidersCoexist(t *testing.T) {
	app := aemeath.New()
	google := oauth2.New("google", &xoauth2.Config{ClientID: "g"}, "https://example.com/g")
	github := oauth2.New("github", &xoauth2.Config{ClientID: "h"}, "https://example.com/h")
	for _, p := range []*oauth2.Plugin{google, github} {
		if err := app.RegisterPlugin(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.RegisterPlugin(oauth2.New("google", &xoauth2.Config{}, "")); err == nil {
		t.Fatal("registering the same provider twice must fail")
	}

	app.Handle("GET", "/p", func(c *aemeath.Ctx) error {
		g, okG := oauth2.From(c, "google")
		h, okH := oauth2.From(c, "github")
		_, okX := oauth2.From(c, "nope")
		if !okG || !okH || okX || g.Config.ClientID != "g" || h.Config.ClientID != "h" {
			t.Errorf("lookup failed: %v %v %v", okG, okH, okX)
		}
		return nil
	})
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/p", nil))
}
