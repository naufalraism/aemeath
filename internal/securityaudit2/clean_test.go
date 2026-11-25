package securityaudit2

// Places checked in this audit that held up. These tests pass; they are here
// so the claim "checked, safe" has evidence and stays true.

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

func TestClean_LoggerCannotBeMadeToForgeLines(t *testing.T) {
	var buf bytes.Buffer
	app := aemeath.New()
	app.SetLogger(slog.New(slog.NewTextHandler(&buf, nil))) // the access log goes to the app's logger
	app.Use(aemeath.Logger())
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/a%0d%0a2026/01/01%20GET%20/admin%20200%201ms", nil))
	t.Logf("log output: %q", buf.String())
	if buf.Len() == 0 {
		t.Fatal("nothing was logged: the test checks nothing")
	}
	if lines := strings.Count(strings.TrimSpace(buf.String()), "\n") + 1; lines != 1 {
		t.Errorf("one request wrote %d log lines", lines)
	}
}

func TestClean_RealIPIgnoresForgedForwardedFor(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RealIP("10.0.0.0/8"))
	var seen string
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { seen = c.R.RemoteAddr; return nil })
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:4000"                          // the trusted proxy
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.7") // client forged 1.2.3.4; the proxy appended the real one
	app.ServeHTTP(httptest.NewRecorder(), req)
	t.Logf("X-Forwarded-For %q via trusted proxy -> client %q", req.Header.Get("X-Forwarded-For"), seen)
	if !strings.HasPrefix(seen, "203.0.113.7") {
		t.Errorf("client taken as %q", seen)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.9:4000" // not a proxy: headers must be ignored
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	app.ServeHTTP(httptest.NewRecorder(), req)
	t.Logf("X-Forwarded-For from an untrusted address -> client %q", seen)
	if !strings.HasPrefix(seen, "198.51.100.9") {
		t.Errorf("untrusted client set its own address: %q", seen)
	}
}

type countingStore struct {
	loads atomic.Int32
	ids   []string
}

func (s *countingStore) Load(_ context.Context, id string) (map[string]any, bool, error) {
	s.loads.Add(1)
	s.ids = append(s.ids, id)
	return nil, false, nil
}
func (s *countingStore) Save(context.Context, string, map[string]any, time.Duration) error {
	return nil
}
func (s *countingStore) Delete(context.Context, string) error { return nil }

func TestClean_SessionCookieOfWrongShapeNeverReachesTheStore(t *testing.T) {
	store := &countingStore{}
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: store}))
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { _ = session.From(c).String("x"); return nil })
	for _, v := range []string{strings.Repeat("A", 100_000), "short", "*", strings.Repeat("A", 43)} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Cookie", "__Host-aemeath_session="+v)
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
	t.Logf("4 requests (100 KB, short, '*', 43 chars) -> store.Load called %d time(s)", store.loads.Load())
	if store.loads.Load() != 1 {
		t.Errorf("store.Load called %d times; only the well-formed id should reach it", store.loads.Load())
	}
}
