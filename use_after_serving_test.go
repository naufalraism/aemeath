package aemeath_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// Use after the first request applied to routes not yet visited and silently
// not to the others.
func TestUseAfterServingPanics(t *testing.T) {
	noop := func(next aemeath.HandlerFunc) aemeath.HandlerFunc { return next }
	app := aemeath.New()
	g := app.Group("/g")
	app.Use(noop) // before serving: fine
	g.Use(noop)
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	for name, fn := range map[string]func(){"App.Use": func() { app.Use(noop) }, "Group.Use": func() { g.Use(noop) }} {
		func() {
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, name) {
					t.Errorf("%s after serving: panic = %q, want a message naming %s", name, msg, name)
				}
			}()
			fn()
		}()
	}
}
