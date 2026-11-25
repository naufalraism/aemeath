package oauth2_test

import (
	"net/http/httptest"
	"testing"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/oauth2"
)

func TestSeveralProvidersCoexist(t *testing.T) {
	app := aemeath.New()
	google := oauth2.New(oauth2.Config{Name: "google", OAuth2: &xoauth2.Config{ClientID: "g"}, UserInfoURL: "https://example.com/g"})
	github := oauth2.New(oauth2.Config{Name: "github", OAuth2: &xoauth2.Config{ClientID: "h"}, UserInfoURL: "https://example.com/h"})
	for _, p := range []*oauth2.Plugin{google, github} {
		if err := app.RegisterPlugin(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.RegisterPlugin(oauth2.New(oauth2.Config{Name: "google", OAuth2: &xoauth2.Config{}, UserInfoURL: "https://example.com/g2"})); err == nil {
		t.Fatal("registering the same provider twice must fail")
	}

	app.Handle("GET", "/p", func(c *aemeath.Ctx) error {
		g, okG := oauth2.From(c, "google")
		h, okH := oauth2.From(c, "github")
		_, okX := oauth2.From(c, "nope")
		if !okG || !okH || okX || g != google || h != github {
			t.Errorf("lookup failed: %v %v %v", okG, okH, okX)
		}
		return nil
	})
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/p", nil))
}
