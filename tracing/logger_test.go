package tracing_test

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/tracing"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// A failed export is logged through the app's logger once the tracer is
// registered as a plugin, or through Config.Logger, not the standard log.
func TestExportFailuresUseTheAppLogger(t *testing.T) {
	var std lockedBuf
	prev := log.Writer()
	log.SetOutput(&std)
	t.Cleanup(func() { log.SetOutput(prev) })

	exportOnce := func(tr *tracing.Tracer) {
		app := aemeath.New()
		app.Use(tr.Middleware())
		app.Handle("GET", "/", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		_ = tr.Flush(context.Background())
		_ = tr.Shutdown(context.Background())
	}

	t.Run("plugin: the app's logger", func(t *testing.T) {
		var logs lockedBuf
		tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: "http://127.0.0.1:1/v1/traces"})
		app := aemeath.New()
		app.SetLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
		if err := app.RegisterPlugin(tr); err != nil {
			t.Fatal(err)
		}
		exportOnce(tr)
		if !strings.Contains(logs.String(), "aemeath/tracing") {
			t.Errorf("app logger got %q", logs.String())
		}
	})
	t.Run("Config.Logger", func(t *testing.T) {
		var logs lockedBuf
		tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: "http://127.0.0.1:1/v1/traces", Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
		exportOnce(tr)
		if !strings.Contains(logs.String(), "aemeath/tracing") {
			t.Errorf("Config.Logger got %q", logs.String())
		}
	})
	if strings.Contains(std.String(), "aemeath/tracing") {
		t.Errorf("the standard log got: %q", std.String())
	}
}
