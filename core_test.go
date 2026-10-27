package aemeath_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type fakeDB struct{ name string }

type greeter interface{ Greet() string }
type english struct{}

func (english) Greet() string { return "hello" }

func TestProvideAndGet(t *testing.T) {
	app := aemeath.New()
	aemeath.Provide(app, &fakeDB{name: "main"})
	aemeath.Provide[greeter](app, english{})

	app.Handle("GET", "/x", func(c *aemeath.Ctx) error {
		db := aemeath.MustGet[*fakeDB](c)
		g, ok := aemeath.Get[greeter](c)
		if !ok {
			t.Error("interface service not found")
		}
		if _, ok := aemeath.Get[*int](c); ok {
			t.Error("an unregistered type must not be found")
		}
		return c.JSON(200, map[string]string{"db": db.name, "greet": g.Greet()})
	})
	if rec := get(app, "/x"); strings.TrimSpace(rec.Body.String()) != `{"db":"main","greet":"hello"}` {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestMustGetPanicsWithAClearMessageAndRecoverTurnsItInto500(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.Recover())
	app.Handle("GET", "/x", func(c *aemeath.Ctx) error {
		_ = aemeath.MustGet[*fakeDB](c)
		return nil
	})
	if rec := get(app, "/x"); rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "fakeDB") {
			t.Fatalf("panic message = %v", r)
		}
	}()
	aemeath.MustGet[*fakeDB](&aemeath.Ctx{})
}

func TestHandledOutputLetsTheHandlerWriteTheResponse(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/old", func(c *aemeath.Ctx, _ none) (aemeath.Handled, error) {
		return aemeath.Handled{}, c.Redirect(http.StatusMovedPermanently, "/new")
	})
	aemeath.GET(app, "/cookie", func(c *aemeath.Ctx, _ none) (aemeath.Handled, error) {
		c.SetCookie(&http.Cookie{Name: "s", Value: "1", HttpOnly: true})
		return aemeath.Handled{}, c.Text(200, "plain")
	})
	rec := get(app, "/old")
	if rec.Code != 301 || rec.Header().Get("Location") != "/new" {
		t.Fatalf("redirect = %d %v", rec.Code, rec.Header())
	}
	rec = get(app, "/cookie")
	if rec.Body.String() != "plain" || !strings.Contains(rec.Header().Get("Set-Cookie"), "s=1") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("response = %v %q", rec.Header(), rec.Body)
	}
}

func TestOnErrorReplacesTheErrorResponder(t *testing.T) {
	app := aemeath.New()
	app.OnError(func(c *aemeath.Ctx, err error) {
		he := aemeath.AsHTTPError(err)
		_ = c.JSON(he.Code, map[string]any{"problem": he.Message, "status": he.Code})
	})
	aemeath.GET(app, "/x", func(*aemeath.Ctx, none) (none, error) { return none{}, aemeath.Forbidden("no entry") })

	for path, want := range map[string]string{
		"/x":       `{"problem":"no entry","status":403}`,
		"/missing": `{"problem":"route not found","status":404}`,
	} {
		if rec := get(app, path); strings.TrimSpace(rec.Body.String()) != want {
			t.Errorf("%s: body = %s, want %s", path, rec.Body, want)
		}
	}
	if rec := do(app, "POST", "/x", ""); !strings.Contains(rec.Body.String(), `"status":405`) {
		t.Errorf("405 must use the custom responder too: %s", rec.Body)
	}
}

func TestSSEStreamsEvents(t *testing.T) {
	app := aemeath.New()
	app.Handle("GET", "/events", func(c *aemeath.Ctx) error {
		sse, err := c.SSE()
		if err != nil {
			return err
		}
		if err := sse.Comment("hi\nthere"); err != nil {
			return err
		}
		if err := sse.Send("tick", map[string]int{"n": 1}); err != nil {
			return err
		}
		if err := sse.Send("", "plain"); err != nil {
			return err
		}
		if err := sse.Send("bad\nname", 1); err == nil {
			t.Error("an event name with a line break must be rejected")
		}
		return nil
	})
	rec := get(app, "/events")
	want := ": hi there\n\nevent: tick\ndata: {\"n\":1}\n\ndata: \"plain\"\n\n"
	if rec.Body.String() != want {
		t.Fatalf("body = %q, want %q", rec.Body.String(), want)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" || !rec.Flushed {
		t.Fatalf("headers = %v flushed = %v", rec.Header(), rec.Flushed)
	}
}

func TestContextHelpers(t *testing.T) {
	var _ = errors.New
	app := aemeath.New()
	app.Handle("GET", "/c", func(c *aemeath.Ctx) error {
		if c.Context() == nil {
			t.Error("Context() is nil")
		}
		return c.JSON(200, c.Param("x"))
	})
}
