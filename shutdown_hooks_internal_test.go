package aemeath

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// All hooks used to share one 10 second limit: a slow hook (jobs draining)
// left the next ones (tracing flush, database close) with an expired context.
func TestEachShutdownHookGetsItsOwnTime(t *testing.T) {
	old := shutdownHookTimeout
	shutdownHookTimeout = 200 * time.Millisecond
	defer func() { shutdownHookTimeout = old }()

	a := New()
	var lastErr error
	var lastLeft time.Duration
	a.OnShutdown(func(ctx context.Context) error { // registered first: runs last
		lastErr = ctx.Err()
		if dl, ok := ctx.Deadline(); ok {
			lastLeft = time.Until(dl)
		}
		return nil
	})
	a.OnShutdown(func(ctx context.Context) error { // runs first and uses all its time
		<-ctx.Done()
		return ctx.Err()
	})
	a.runShutdownHooks()
	if lastErr != nil || lastLeft < 100*time.Millisecond {
		t.Fatalf("the hook after a slow one started with ctx.Err()=%v and %v left; want a fresh limit", lastErr, lastLeft)
	}
}

func TestStopIsIdempotentAndClosesStopping(t *testing.T) {
	a := New()
	select {
	case <-a.Stopping():
		t.Fatal("Stopping is closed before Stop")
	default:
	}
	a.Stop()
	a.Stop()
	select {
	case <-a.Stopping():
	default:
		t.Fatal("Stopping is not closed after Stop")
	}
}

// The file logger's file was never closed: Close existed but nothing called it.
func TestFileLoggerIsClosedAtShutdown(t *testing.T) {
	p, err := NewFileLogger(t.TempDir() + "/app.log")
	if err != nil {
		t.Fatal(err)
	}
	a := New()
	if err := a.RegisterPlugin(p); err != nil {
		t.Fatal(err)
	}
	a.runShutdownHooks()
	if _, err := p.File.WriteString("x"); err == nil {
		t.Fatal("the log file is still open after the shutdown hooks ran")
	}
}

// FileLogger is deprecated in favour of SlogLogger writing to a file, as its
// doc comment shows. That recipe must log what FileLogger logged (time,
// method, path, status, duration, client address), plus the request ID, and
// the file must be closed by the shutdown hook.
func TestSlogLoggerToAFileReplacesFileLogger(t *testing.T) {
	path := t.TempDir() + "/access.log"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	a := New()
	a.Use(RequestID(), SlogLogger(slog.New(slog.NewJSONHandler(f, nil))))
	a.OnShutdown(func(context.Context) error { return f.Close() })
	a.Handle("GET", "/items/{id}", func(c *Ctx) error { return NotFound("no such item") })

	r := httptest.NewRequest("GET", "/items/7", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	a.ServeHTTP(httptest.NewRecorder(), r)
	a.runShutdownHooks()

	if _, err := f.WriteString("x"); err == nil {
		t.Error("the log file is still open after the shutdown hooks ran")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var line map[string]any
	if err := json.Unmarshal(data, &line); err != nil {
		t.Fatalf("not one JSON line: %q (%v)", data, err)
	}
	for _, k := range []string{"time", "method", "path", "status", "duration_ms", "remote", "request_id"} {
		if _, ok := line[k]; !ok {
			t.Errorf("the log line has no %q: %s", k, data)
		}
	}
	if line["method"] != "GET" || line["path"] != "/items/7" || line["status"] != float64(404) || line["remote"] != "203.0.113.9:4000" {
		t.Errorf("wrong values: %s", data)
	}
}
