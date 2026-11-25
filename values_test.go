package aemeath_test

import (
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// c.Set and c.Value carry a value from a middleware to the handlers after
// it, for one request. They replace the exported Ctx.State map, which was
// allocated on every request even when nothing used it.
func TestSetAndValueCarryARequestValue(t *testing.T) {
	app := aemeath.New()
	app.Use(func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			if c.Value("tenant") != nil {
				t.Error("a value leaked from an earlier request")
			}
			c.Set("tenant", "acme")
			return next(c)
		}
	})
	aemeath.GET(app, "/t", func(c *aemeath.Ctx, _ struct{}) (string, error) {
		s, _ := c.Value("tenant").(string)
		if c.Value("missing") != nil {
			t.Error(`Value("missing") != nil`)
		}
		return s, nil
	})
	for range 2 {
		if body := strings.TrimSpace(get(app, "/t").Body.String()); body != `"acme"` {
			t.Fatalf("body = %s, want \"acme\"", body)
		}
	}
}

// A request that stores nothing allocates no map for it.
func TestUnusedValuesCostNoAllocation(t *testing.T) {
	use := aemeath.New()
	aemeath.GET(use, "/z", func(c *aemeath.Ctx, _ struct{}) (string, error) { c.Set("k", 1); return "z", nil })
	none := aemeath.New()
	aemeath.GET(none, "/z", func(c *aemeath.Ctx, _ struct{}) (string, error) { return "z", nil })
	allocs := func(a *aemeath.App) float64 {
		return testing.AllocsPerRun(100, func() { get(a, "/z") })
	}
	if u, n := allocs(use), allocs(none); u <= n {
		t.Errorf("a request that calls Set allocates %.0f, one that does not %.0f: the map is made even when unused", u, n)
	}
}
