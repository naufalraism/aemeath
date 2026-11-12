# Metrics and tracing

Neither package adds a third-party dependency, and both speak open standards, so they work with Prometheus, Grafana, Jaeger, Tempo, Honeycomb and any OpenTelemetry Collector.

## Metrics (Prometheus)

```go
import "github.com/naufalraism/aemeath/metrics"

reg := metrics.New()
app.Use(reg.Middleware())                    // first, so it sees every request
app.Handle("GET", "/metrics", reg.Handler())
```

You get `http_requests_total{method,route,status}`, `http_request_duration_seconds` (histogram) and `http_requests_in_flight`.

- The `route` label is the route **pattern** (`/users/{id}`), never the raw path, so cardinality stays small. Requests that match no route share one `unmatched` series, so a scanner probing thousands of URLs cannot flood your metrics.
- Every metric is also capped at 2000 label combinations; extras are folded into an `overflow` series.
- `/metrics` lists your routes and traffic. **Do not expose it to the internet**: serve it on an internal port, or protect it with a middleware.

Custom metrics:

```go
jobs := reg.Counter("jobs_total", "Jobs processed.", "kind")
jobs.Inc("email")
depth := reg.Gauge("queue_depth", "Jobs waiting.")
depth.Set(12)
lat := reg.Histogram("job_seconds", "Job time.", []float64{.1, .5, 1, 5})
lat.Observe(0.42)
```

Never use unbounded values (user ids, URLs, error messages) as label values.

## Tracing (W3C Trace Context + OTLP)

```go
import "github.com/naufalraism/aemeath/tracing"

tr := tracing.New(tracing.Config{
	ServiceName: "api",
	Endpoint:    "http://localhost:4318/v1/traces", // OTLP/HTTP, JSON encoding
})
app.Use(tr.Middleware())
app.RegisterPlugin(tr) // flushes pending spans on graceful shutdown
```

Every request becomes a server span named `GET /users/{id}`. An incoming `traceparent` header continues the caller's trace (including its sampled flag); a malformed one starts a fresh trace. Create child spans around slow work:

```go
ctx, span := tr.Start(c.Context(), "db.query")
defer span.End()
span.SetAttr("db.system", "postgres")
rows, err := queryWith(ctx)
if err != nil {
	span.SetError()
}
```

To continue the trace into another service, send `span.Traceparent()` as the `traceparent` header.

Guarantees:

- Tracing **never blocks or fails a request**. The queue is bounded (`QueueSize`, default 2048); when the collector is slow or down, spans are dropped and counted (`tr.Dropped()`).
- Only the path is recorded (`url.path`), never the query string, which often carries tokens. Do not put secrets or personal data in attributes.
- `SampleRatio` (default 1) samples new traces; the decision of an incoming trace is honored.

This is an exporter for the OTLP/HTTP protocol, not the OpenTelemetry SDK. It covers HTTP server spans, child spans and propagation. If you need auto-instrumentation of other libraries, use the official SDK; you can run both against the same Collector.
