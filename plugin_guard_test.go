package aemeath_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type guardDB struct{ name string }

type guardPlugin struct {
	db      *guardDB
	applied int
}

func (p *guardPlugin) Apply(app *aemeath.App) error {
	p.applied++
	aemeath.Provide(app, p.db)
	return nil
}

// Registering the same plugin twice ran Apply twice: two readiness checks,
// two shutdown hooks closing the same database. It is now an error.
func TestRegisterPluginRefusesTheSamePluginTwice(t *testing.T) {
	app := aemeath.New()
	p := &guardPlugin{db: &guardDB{"main"}}
	if err := app.RegisterPlugin(p); err != nil {
		t.Fatal(err)
	}
	err := app.RegisterPlugin(p)
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("second RegisterPlugin: %v, want an error saying it is already registered", err)
	}
	if p.applied != 1 {
		t.Errorf("Apply ran %d times, want 1", p.applied)
	}
	// Two different plugins of the same type are two plugins.
	if err := app.RegisterPlugin(&guardPlugin{db: &guardDB{"other"}}); err != nil {
		t.Errorf("a second plugin of the same type: %v", err)
	}
}

// A plugin that is not a pointer cannot be told apart from another equal one;
// it is registered as before.
type valuePlugin struct{}

func (valuePlugin) Apply(*aemeath.App) error { return nil }

func TestRegisterPluginAcceptsValuePlugins(t *testing.T) {
	app := aemeath.New()
	for i := 0; i < 2; i++ {
		if err := app.RegisterPlugin(valuePlugin{}); err != nil {
			t.Fatalf("value plugin %d: %v", i, err)
		}
	}
}

// Providing a second value of the same type silently replaced the first:
// with a main and an analytics database registered in that order, every
// handler read the analytics one. The replacement now logs a warning.
func TestProvideWarnsWhenItReplacesAValue(t *testing.T) {
	var logs logBuf
	app := aemeath.New()
	app.SetLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	_ = app.RegisterPlugin(&guardPlugin{db: &guardDB{"main"}})
	if logs.String() != "" {
		t.Fatalf("first Provide logged: %s", logs.String())
	}
	_ = app.RegisterPlugin(&guardPlugin{db: &guardDB{"analytics"}})
	l := logs.lines(t)
	if len(l) != 1 || l[0]["level"] != "WARN" || !strings.Contains(l[0]["msg"].(string), "replaces") || !strings.Contains(l[0]["type"].(string), "guardDB") {
		t.Errorf("want one warning naming the type, got %s", logs.String())
	}
}

// Two readiness checks with the same name were folded into one entry of
// /readyz, which could read "ok" next to a 503. Each check now has its own.
func TestReadyChecksWithTheSameNameAreAllShown(t *testing.T) {
	app := aemeath.New()
	app.AddReadyCheck("database", func(context.Context) error { return errors.New("analytics down") })
	app.AddReadyCheck("database", func(context.Context) error { return nil })
	app.AddReadyCheck("database", func(context.Context) error { return nil })
	app.EnableHealth()
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	want := `{"checks":{"database":"failed","database (2)":"ok","database (3)":"ok"},"status":"unavailable"}`
	if got := strings.TrimSpace(w.Body.String()); w.Code != 503 || got != want {
		t.Errorf("%d %s\nwant 503 %s", w.Code, got, want)
	}
}
