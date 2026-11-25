package tracing_test

import (
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/tracing"
)

// A span's identity is read through methods: the fields were exported and
// writable, so code could change a TraceID halfway through a request and
// break the trace it exports.
func TestSpanIdentityIsReadOnly(t *testing.T) {
	tr := tracing.New(tracing.Config{ServiceName: "t", Endpoint: "http://127.0.0.1:1"})
	app := aemeath.New()
	app.Use(tr.Middleware())
	var got *tracing.Span
	app.Handle("GET", "/x", func(c *aemeath.Ctx) error {
		got = tracing.FromContext(c.Context())
		return c.Text(200, "x")
	})
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	app.ServeHTTP(httptest.NewRecorder(), r)

	if got == nil {
		t.Fatal("no span in the request's context")
	}
	if got.TraceID() != "4bf92f3577b34da6a3ce929d0e0e4736" || got.ParentID() != "00f067aa0ba902b7" {
		t.Errorf("TraceID %q, ParentID %q: not the incoming traceparent", got.TraceID(), got.ParentID())
	}
	if len(got.SpanID()) != 16 || got.Name() == "" || got.Start().IsZero() {
		t.Errorf("SpanID %q, Name %q, Start %v", got.SpanID(), got.Name(), got.Start())
	}
}

// The middleware names the span after the route is matched, while code the
// handler started may be reading the name: Name is safe to call from any
// goroutine (run with -race).
func TestSpanNameCanBeReadWhileTheRouteNamesIt(t *testing.T) {
	tr := tracing.New(tracing.Config{ServiceName: "t", Endpoint: "http://127.0.0.1:1"})
	app := aemeath.New()
	app.Use(tr.Middleware())
	done, stopped, reading := make(chan struct{}), make(chan struct{}), make(chan struct{})
	app.Handle("GET", "/users/{id}", func(c *aemeath.Ctx) error {
		span := tracing.FromContext(c.Context())
		go func() {
			defer close(stopped)
			_ = span.Name()
			close(reading)
			for {
				select {
				case <-done:
					return
				default:
					_ = span.Name()
				}
			}
		}()
		<-reading // the name is read before the middleware sets it
		return c.Text(200, "x")
	})
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/7", nil))
	close(done)
	<-stopped
}
