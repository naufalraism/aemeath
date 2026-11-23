package tracing_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/tracing"
)

type none struct{}

type collector struct {
	mu     sync.Mutex
	bodies []map[string]any
	auth   string
	srv    *httptest.Server
}

func newCollector(t *testing.T, status int) *collector {
	c := &collector{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		c.mu.Lock()
		c.bodies = append(c.bodies, m)
		c.auth = r.Header.Get("Authorization")
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *collector) spans() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, b := range c.bodies {
		for _, rs := range b["resourceSpans"].([]any) {
			for _, ss := range rs.(map[string]any)["scopeSpans"].([]any) {
				for _, s := range ss.(map[string]any)["spans"].([]any) {
					out = append(out, s.(map[string]any))
				}
			}
		}
	}
	return out
}

func newApp(tr *tracing.Tracer) *aemeath.App {
	app := aemeath.New()
	app.Use(tr.Middleware())
	aemeath.GET(app, "/users/{id}", func(c *aemeath.Ctx, _ struct {
		ID string `path:"id"`
	}) (map[string]string, error) {
		_, child := tr.Start(c.Context(), "db.query")
		child.SetAttr("db.system", "postgres")
		child.End()
		return map[string]string{"ok": "1"}, nil
	})
	aemeath.GET(app, "/boom", func(c *aemeath.Ctx, _ none) (none, error) { return none{}, context.DeadlineExceeded })
	return app
}

func get(app *aemeath.App, path string, h map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for k, v := range h {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

const parentTrace = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestExportsServerAndChildSpansWithPropagation(t *testing.T) {
	col := newCollector(t, 200)
	tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: col.srv.URL, Headers: map[string]string{"Authorization": "Bearer k"}})
	app := newApp(tr)
	get(app, "/users/42?token=secret", map[string]string{"traceparent": "00-" + parentTrace + "-00f067aa0ba902b7-01"})
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	sp := col.spans()
	if len(sp) != 2 {
		t.Fatalf("want 2 spans, got %d", len(sp))
	}
	var server, child map[string]any
	for _, s := range sp {
		if s["name"] == "db.query" {
			child = s
		} else {
			server = s
		}
	}
	if server["traceId"] != parentTrace || server["parentSpanId"] != "00f067aa0ba902b7" || server["name"] != "GET /users/{id}" {
		t.Fatalf("server span: %v", server)
	}
	if child["traceId"] != parentTrace || child["parentSpanId"] != server["spanId"] {
		t.Fatalf("child not linked to server span: %v", child)
	}
	if col.auth != "Bearer k" {
		t.Fatal("headers not sent")
	}
	if b, _ := json.Marshal(sp); strings.Contains(string(b), "secret") {
		t.Fatal("query string leaked into span attributes")
	}
}

func TestSamplingFlagIsHonoredAndErrorsMarked(t *testing.T) {
	col := newCollector(t, 200)
	tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: col.srv.URL})
	app := newApp(tr)
	get(app, "/users/1", map[string]string{"traceparent": "00-" + parentTrace + "-00f067aa0ba902b7-00"}) // parent says: not sampled
	get(app, "/boom", nil)
	_ = tr.Flush(context.Background())
	sp := col.spans()
	if len(sp) != 1 || sp[0]["name"] != "GET /boom" {
		t.Fatalf("unsampled trace was exported or boom missing: %v", sp)
	}
	st, _ := sp[0]["status"].(map[string]any)
	if st["code"] != float64(2) {
		t.Fatalf("5xx must set error status: %v", sp[0])
	}
}

func TestInvalidIncomingHeaderStartsAFreshTrace(t *testing.T) {
	col := newCollector(t, 200)
	tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: col.srv.URL})
	get(newApp(tr), "/boom", map[string]string{"traceparent": "junk"})
	_ = tr.Flush(context.Background())
	sp := col.spans()
	if len(sp) != 1 || len(sp[0]["traceId"].(string)) != 32 || sp[0]["parentSpanId"] != nil {
		t.Fatalf("%v", sp)
	}
}

func TestSlowCollectorNeverBreaksRequestsAndQueueIsBounded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: srv.URL, QueueSize: 4, BatchSize: 1, FlushInterval: time.Hour,
		Client: &http.Client{Timeout: 50 * time.Millisecond}})
	app := newApp(tr)
	start := time.Now()
	for i := 0; i < 200; i++ {
		if w := get(app, "/users/1", nil); w.Code != 200 {
			t.Fatalf("request failed because the collector is slow: %d", w.Code)
		}
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("requests were slowed down by the exporter")
	}
	if tr.Dropped() == 0 {
		t.Fatal("expected dropped spans once the bounded queue filled")
	}
}

func TestShutdownFlushesAndIsIdempotent(t *testing.T) {
	col := newCollector(t, 200)
	tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: col.srv.URL, FlushInterval: time.Hour})
	get(newApp(tr), "/users/1", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tr.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tr.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if len(col.spans()) != 2 {
		t.Fatalf("shutdown did not flush: %d", len(col.spans()))
	}
}

func TestBadConfigPanics(t *testing.T) {
	for name, cfg := range map[string]tracing.Config{
		"no name":      {},
		"bad endpoint": {ServiceName: "x", Endpoint: "ftp://x"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected panic", name)
				}
			}()
			tracing.New(cfg)
		}()
	}
}

// tracing.New used to start its exporter goroutine at once, also without an
// Endpoint; a Tracer that was never shut down kept it for ever.
func TestNoGoroutineUntilTheFirstSpan(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		tr := tracing.New(tracing.Config{ServiceName: "x"}) // no Endpoint
		_, s := tr.Start(context.Background(), "op")
		s.End()
		_ = tracing.New(tracing.Config{ServiceName: "x", Endpoint: "http://127.0.0.1:1/v1/traces"}) // never used
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines grew from %d to %d", before, after)
	}
}

// Flush after Shutdown waited for an exporter that had already exited: with
// context.Background() it never returned.
func TestFlushAndShutdownNeverHang(t *testing.T) {
	check := func(name string, fn func() error) {
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("%s did not return", name)
		}
	}
	bg := context.Background()

	idle := tracing.New(tracing.Config{ServiceName: "x", Endpoint: "http://127.0.0.1:1/v1/traces"})
	check("Flush on a tracer that never had a span", func() error { return idle.Flush(bg) })
	check("Shutdown on a tracer that never had a span", func() error { return idle.Shutdown(bg) })
	check("Flush after Shutdown", func() error { return idle.Flush(bg) })
	check("second Shutdown", func() error { return idle.Shutdown(bg) })

	used := tracing.New(tracing.Config{ServiceName: "x", Endpoint: "http://127.0.0.1:1/v1/traces"})
	_, s := used.Start(bg, "op")
	s.End()
	check("Shutdown with a queued span", func() error { return used.Shutdown(bg) })
	check("Flush after Shutdown (used tracer)", func() error { return used.Flush(bg) })
	_, s = used.Start(bg, "late")
	s.End() // after Shutdown: ignored, must not start the exporter again
}

// A client can send traceparent "...-01" to have its request sampled and
// exported whatever SampleRatio says (B3 of the security audit). With
// IgnoreIncomingSampling the decision is made here; the trace ID still
// continues.
func TestIgnoreIncomingSampling(t *testing.T) {
	forced := map[string]string{"traceparent": "00-" + parentTrace + "-00f067aa0ba902b7-01"}
	for _, ignore := range []bool{false, true} {
		col := newCollector(t, 200)
		tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: col.srv.URL, SampleRatio: 1e-9, IgnoreIncomingSampling: ignore})
		app := newApp(tr)
		for i := 0; i < 50; i++ {
			get(app, "/users/1", forced)
		}
		_ = tr.Flush(context.Background())
		sp := col.spans()
		t.Logf("IgnoreIncomingSampling=%v, SampleRatio=1e-9, 50 requests with sampled=01: %d spans exported", ignore, len(sp))
		if !ignore && len(sp) == 0 {
			t.Error("by default an incoming sampled decision must be honored")
		}
		if ignore && len(sp) != 0 {
			t.Errorf("with IgnoreIncomingSampling the client still forced %d spans", len(sp))
		}
	}
	// The trace continues: same trace ID, the caller as parent.
	tr := tracing.New(tracing.Config{ServiceName: "svc", SampleRatio: 1e-9, IgnoreIncomingSampling: true})
	app := aemeath.New()
	app.Use(tr.Middleware())
	var got *tracing.Span
	app.Handle("GET", "/x", func(c *aemeath.Ctx) error { got = tracing.FromContext(c.Context()); return nil })
	get(app, "/x", forced)
	if got == nil || got.TraceID != parentTrace || got.ParentID != "00f067aa0ba902b7" || got.Sampled() {
		t.Errorf("span: %+v", got)
	}
}
