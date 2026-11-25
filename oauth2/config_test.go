package oauth2_test

import (
	"strings"
	"testing"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath/oauth2"
)

func panicOf(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg, _ = r.(string)
			if msg == "" {
				msg = "panic"
			}
		}
	}()
	f()
	return ""
}

// The provider is configured once, through oauth2.Config like oidc.Config:
// the plugin's fields were exported and read on every request, so changing
// one after the server started raced with logins. A config that cannot log
// anybody in stops the server at startup.
func TestNewTakesOneConfigAndRefusesAnIncompleteOne(t *testing.T) {
	ok := oauth2.Config{Name: "github", OAuth2: &xoauth2.Config{ClientID: "x"}, UserInfoURL: "https://api.github.com/user", PKCE: true}
	if msg := panicOf(func() { oauth2.New(ok) }); msg != "" {
		t.Fatalf("a complete config panicked: %q", msg)
	}
	for field, cfg := range map[string]oauth2.Config{
		"Name":        {OAuth2: ok.OAuth2, UserInfoURL: ok.UserInfoURL},
		"OAuth2":      {Name: "github", UserInfoURL: ok.UserInfoURL},
		"UserInfoURL": {Name: "github", OAuth2: ok.OAuth2},
	} {
		if msg := panicOf(func() { oauth2.New(cfg) }); !strings.Contains(msg, field) {
			t.Errorf("config without %s: panic %q, want a message naming it", field, msg)
		}
	}
}
