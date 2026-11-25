package aemeath_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/metrics"
)

// syncBuf is a bytes.Buffer safe for the logger and the test to share.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// lines decodes the JSON lines written to the app's logger.
func (l *logBuf) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(l.String()), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("not a JSON line: %q", ln)
		}
		out = append(out, m)
	}
	return out
}

// captureStdLog sends the standard log package to a buffer for the test.
func captureStdLog(t *testing.T) *logBuf {
	t.Helper()
	b := &logBuf{}
	prev := log.Writer()
	log.SetOutput(b)
	t.Cleanup(func() { log.SetOutput(prev) })
	return b
}

func jsonLoggerApp(t *testing.T) (*aemeath.App, *logBuf) {
	t.Helper()
	b := &logBuf{}
	app := aemeath.New()
	app.SetLogger(slog.New(slog.NewJSONHandler(b, nil)))
	return app, b
}

// The cause of a 500 was logged once more by every middleware that asked
// for the status (SlogLogger, metrics, tracing, an OnError handler), to the
// standard log, without the request id: one database error produced several
// identical lines that could not be matched to their request. It is now one
// line in the app's logger, with the request id, method and path.
func TestAnInternalErrorIsLoggedOnceWithItsRequest(t *testing.T) {
	std := captureStdLog(t)
	app, logs := jsonLoggerApp(t)
	reg := metrics.New()
	app.Use(aemeath.RequestID(), aemeath.SlogLogger(slog.New(slog.NewJSONHandler(&logBuf{}, nil))), reg.Middleware())
	var onErrorStatus int
	app.OnError(func(c *aemeath.Ctx, err error) {
		he := aemeath.AsHTTPError(err)
		onErrorStatus = he.Code
		_ = c.JSON(he.Code, he)
	})
	app.Handle("GET", "/orders/{id}", func(c *aemeath.Ctx) error { return errors.New("db connection refused") })

	r := httptest.NewRequest("GET", "/orders/7", nil)
	r.Header.Set("X-Request-ID", "req-42")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 500 || onErrorStatus != 500 || strings.Contains(w.Body.String(), "refused") {
		t.Fatalf("response %d %s (OnError saw %d); the cause must not reach the client", w.Code, w.Body.String(), onErrorStatus)
	}
	var found []map[string]any
	for _, l := range logs.lines(t) {
		if strings.Contains(l["error"].(string), "db connection refused") {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the cause was logged %d times, want once:\n%s", len(found), logs.String())
	}
	l := found[0]
	if l["level"] != "ERROR" || l["request_id"] != "req-42" || l["method"] != "GET" || l["path"] != "/orders/7" {
		t.Errorf("the line lacks the request: %v", l)
	}
	if strings.Contains(std.String(), "refused") {
		t.Errorf("the standard log got it too: %q", std.String())
	}
}

// Every message of the framework goes to the app's logger, none to the
// standard log package.
func TestFrameworkMessagesGoToTheAppLogger(t *testing.T) {
	std := captureStdLog(t)
	app, logs := jsonLoggerApp(t)
	app.Use(aemeath.RequestID(), aemeath.Recover())

	type getWithBody struct {
		Name string `json:"name"` // a GET has no body: a warning at registration
	}
	aemeath.GET(app, "/warn", func(*aemeath.Ctx, getWithBody) (string, error) { return "", nil })
	app.Handle("GET", "/late", func(c *aemeath.Ctx) error {
		_ = c.Text(200, "partial")
		return errors.New("failed after sending")
	})
	app.Handle("GET", "/badjson", func(c *aemeath.Ctx) error { return c.JSON(200, map[string]any{"ch": make(chan int)}) })
	app.Handle("GET", "/panic", func(*aemeath.Ctx) error { panic("boom") })
	app.AddReadyCheck("database", func(context.Context) error { return errors.New("connection reset") })
	app.EnableHealth()

	for _, p := range []string{"/late", "/badjson", "/panic", "/readyz"} {
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("X-Request-ID", "id"+strings.ReplaceAll(p, "/", "-"))
		app.ServeHTTP(httptest.NewRecorder(), r)
	}

	want := map[string]string{ // message fragment -> request id ("" = not a request)
		"has a json tag but":                "",
		"error after the response was sent": "id-late",
		"encoding the JSON response failed": "id-badjson",
		"panic":                             "id-panic",
		"readiness check failed":            "id-readyz",
	}
	all := logs.lines(t)
	for frag, id := range want {
		var hit map[string]any
		for _, l := range all {
			if strings.Contains(l["msg"].(string), frag) {
				hit = l
			}
		}
		if hit == nil {
			t.Errorf("no app-logger line with %q:\n%s", frag, logs.String())
			continue
		}
		if id != "" && hit["request_id"] != id {
			t.Errorf("%q: request_id = %v, want %s", frag, hit["request_id"], id)
		}
	}
	if s := std.String(); s != "" {
		t.Errorf("the standard log still got:\n%s", s)
	}
}

// Without SetLogger the messages still appear, through slog.Default (which
// writes through the standard log package unless the program changed it).
func TestWithoutSetLoggerMessagesStillAppear(t *testing.T) {
	std := captureStdLog(t)
	app := aemeath.New()
	app.Handle("GET", "/boom", func(*aemeath.Ctx) error { return errors.New("db connection refused") })
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/boom", nil))
	if n := strings.Count(std.String(), "db connection refused"); n != 1 {
		t.Errorf("logged %d times, want once: %q", n, std.String())
	}
	if !strings.Contains(std.String(), "internal error") {
		t.Errorf("the line lacks \"internal error\": %q", std.String())
	}
}

// c.Logger() is the app's logger with the request id, method and path, for
// handlers that log something of their own.
func TestCtxLoggerCarriesTheRequest(t *testing.T) {
	app, logs := jsonLoggerApp(t)
	app.Use(aemeath.RequestID())
	app.Handle("POST", "/pay", func(c *aemeath.Ctx) error {
		c.Logger().Info("payment accepted", "amount", 5000)
		return c.Text(200, "ok")
	})
	r := httptest.NewRequest("POST", "/pay", nil)
	r.Header.Set("X-Request-ID", "pay-1")
	app.ServeHTTP(httptest.NewRecorder(), r)
	l := logs.lines(t)
	if len(l) != 1 || l[0]["msg"] != "payment accepted" || l[0]["request_id"] != "pay-1" || l[0]["method"] != "POST" || l[0]["path"] != "/pay" || l[0]["amount"] != 5000.0 {
		t.Errorf("got %v", l)
	}
	if app.Logger() == nil {
		t.Error("App.Logger() is nil")
	}
}

var _ = http.StatusOK

// The access log of Logger() is a framework message like any other: it goes
// to the app's logger, so SetLogger(JSON) also makes the access log JSON. It
// used to go to the standard log package as plain text, whatever SetLogger
// chose.
func TestLoggerMiddlewareWritesToTheAppLogger(t *testing.T) {
	std := captureStdLog(t)
	app, logs := jsonLoggerApp(t)
	app.Use(aemeath.RequestID(), aemeath.Logger())
	app.Handle("GET", "/ok", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })
	app.Handle("GET", "/missing", func(c *aemeath.Ctx) error { return aemeath.NotFound("no") })
	app.Handle("GET", "/fail", func(c *aemeath.Ctx) error { return errors.New("boom") })

	for _, p := range []string{"/ok", "/missing", "/fail"} {
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("X-Request-ID", "id"+strings.ReplaceAll(p, "/", "-"))
		app.ServeHTTP(httptest.NewRecorder(), r)
	}
	want := map[string]struct {
		status float64
		level  string
	}{"/ok": {200, "INFO"}, "/missing": {404, "WARN"}, "/fail": {500, "ERROR"}}
	seen := 0
	for _, l := range logs.lines(t) {
		if l["msg"] != "request" {
			continue
		}
		w, ok := want[l["path"].(string)]
		if !ok {
			continue
		}
		seen++
		if l["status"] != w.status || l["level"] != w.level || l["method"] != "GET" || l["request_id"] != "id"+strings.ReplaceAll(l["path"].(string), "/", "-") || l["duration_ms"] == nil {
			t.Errorf("access log line = %v, want status %v at %s with method, request_id and duration_ms", l, w.status, w.level)
		}
	}
	if seen != 3 {
		t.Errorf("%d access log lines in the app's logger, want 3:\n%s", seen, logs.String())
	}
	if s := std.String(); s != "" {
		t.Errorf("the standard log still got:\n%s", s)
	}
}
