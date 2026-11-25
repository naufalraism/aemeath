package session_test

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
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

// The session's messages go to the app's logger, with the request, not to
// the standard log package.
func TestSessionLogsThroughTheAppLogger(t *testing.T) {
	var std lockedBuf
	prev := log.Writer()
	log.SetOutput(&std)
	t.Cleanup(func() { log.SetOutput(prev) })

	full := session.NewMemoryStore().WithMaxSessions(1)
	if err := full.Save(context.Background(), "someone-else", map[string]any{"k": 1}, time.Hour); err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]session.Store{
		"saving failed": failingStore{},
		"store full":    full,
	} {
		t.Run(name, func(t *testing.T) {
			var logs lockedBuf
			app := aemeath.New()
			app.SetLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
			app.Use(aemeath.RequestID(), session.Middleware(session.Config{Store: store, Insecure: true}))
			app.Handle("GET", "/set", func(c *aemeath.Ctx) error { session.From(c).Set("k", 1); return c.Text(200, "ok") })
			r := httptest.NewRequest("GET", "/set", nil)
			r.Header.Set("X-Request-ID", "sess-1")
			app.ServeHTTP(httptest.NewRecorder(), r)
			out := logs.String()
			if !strings.Contains(out, `"request_id":"sess-1"`) || !strings.Contains(out, "aemeath/session") {
				t.Errorf("app logger got %q", out)
			}
		})
	}
	if strings.Contains(std.String(), "aemeath/session") {
		t.Errorf("the standard log got: %q", std.String())
	}
}
