package aemeath_test

import (
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/naufalraism/aemeath"
)

// /docs runs a third-party script on the application's origin. Without an
// integrity hash, whoever controls the CDN response controls that script.
func TestDocsScriptsHaveIntegrity(t *testing.T) {
	app := aemeath.New()
	app.EnableDocs(aemeath.Info{Title: "t", Version: "1"})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/docs", nil))
	tags := regexp.MustCompile(`<script[^>]*\ssrc="[^"]+"[^>]*>`).FindAllString(w.Body.String(), -1)
	if len(tags) == 0 {
		t.Fatal("no external script found in /docs")
	}
	sri := regexp.MustCompile(`integrity="sha(256|384|512)-[A-Za-z0-9+/]+={0,2}"`)
	for _, tag := range tags {
		if !sri.MatchString(tag) || !regexp.MustCompile(`crossorigin="anonymous"`).MatchString(tag) {
			t.Errorf("script without integrity/crossorigin: %s", tag)
		}
	}
}
