// Package tracing adds distributed tracing without third-party dependencies.
//
// It speaks the two open standards that make traces portable:
//
//   - W3C Trace Context (the traceparent header) for propagation, and
//
//   - OTLP/HTTP with JSON encoding for export, which OpenTelemetry Collectors,
//     Jaeger, Tempo, Grafana Cloud, Honeycomb and others accept.
//
//     tr := tracing.New(tracing.Config{ServiceName: "api", Endpoint: "http://localhost:4318/v1/traces"})
//     app.Use(tr.Middleware())
//     app.RegisterPlugin(tr) // flushes pending spans on shutdown
//
// Spans never block or fail a request: when the collector is slow or down, the
// queue is bounded and the oldest data is dropped (see Tracer.Dropped).
package tracing

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/naufalraism/aemeath"
)

// Config configures a Tracer.
type Config struct {
	ServiceName   string            // required; reported as service.name
	Endpoint      string            // OTLP/HTTP traces URL, e.g. http://localhost:4318/v1/traces; empty = trace but do not export
	Headers       map[string]string // for example an API key for a hosted backend
	SampleRatio   float64           // 0..1 for new traces (default 1); incoming sampled decisions are honored
	QueueSize     int               // max buffered spans (default 2048)
	BatchSize     int               // spans per export request (default 256)
	FlushInterval time.Duration     // default 5s
	Client        *http.Client      // default: 5s timeout
}

// Span is one unit of work.
type Span struct {
	TraceID, SpanID, ParentID string
	Name                      string
	Start, EndTime            time.Time
	Attrs                     map[string]string
	Error                     bool
	Sampled                   bool
	kind                      int
	tracer                    *Tracer
	mu                        sync.Mutex
}

// Tracer creates spans and exports them.
type Tracer struct {
	cfg     Config
	queue   chan *Span
	done    chan struct{}
	wg      sync.WaitGroup
	dropped atomic.Uint64
	closed  atomic.Bool
	flushCh chan chan struct{}
}

// New creates a Tracer and starts its exporter goroutine.
func New(cfg Config) *Tracer {
	if cfg.ServiceName == "" {
		panic("tracing: Config.ServiceName is required")
	}
	if cfg.Endpoint != "" && !strings.HasPrefix(cfg.Endpoint, "http://") && !strings.HasPrefix(cfg.Endpoint, "https://") {
		panic("tracing: Endpoint must be an http(s) URL")
	}
	if cfg.SampleRatio <= 0 || cfg.SampleRatio > 1 {
		cfg.SampleRatio = 1
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 2048
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 256
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Second}
	}
	t := &Tracer{cfg: cfg, queue: make(chan *Span, cfg.QueueSize), done: make(chan struct{}), flushCh: make(chan chan struct{})}
	t.wg.Add(1)
	go t.loop()
	return t
}

// Name implements aemeath.Plugin.
func (t *Tracer) Name() string { return "tracing" }

// Apply flushes pending spans when the server shuts down.
func (t *Tracer) Apply(app *aemeath.App) error {
	aemeath.Provide(app, t)
	app.OnShutdown(t.Shutdown)
	return nil
}

// Dropped returns how many spans were discarded because the queue was full.
func (t *Tracer) Dropped() uint64 { return t.dropped.Load() }

func randHex(n int) string {
	b := make([]byte, n)
	for {
		if _, err := rand.Read(b); err != nil {
			panic("tracing: crypto/rand failed: " + err.Error())
		}
		if !allZero(b) { // all-zero ids are invalid in W3C Trace Context
			return hex.EncodeToString(b)
		}
	}
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// isID is isHex for trace and span ids, which must not be all zeroes.
func isID(s string, n int) bool { return isHex(s, n) && s != strings.Repeat("0", n) }

// ParseTraceparent parses a W3C traceparent header ("00-<trace>-<parent>-<flags>").
func ParseTraceparent(h string) (traceID, parentID string, sampled, ok bool) {
	p := strings.Split(strings.TrimSpace(h), "-")
	if len(p) < 4 || len(p[0]) != 2 || p[0] == "ff" {
		return "", "", false, false
	}
	if p[0] == "00" && len(p) != 4 {
		return "", "", false, false
	}
	if !isID(p[1], 32) || !isID(p[2], 16) || !isHex(p[3], 2) {
		return "", "", false, false
	}
	flags, _ := strconv.ParseUint(p[3], 16, 8)
	return p[1], p[2], flags&1 == 1, true
}

// Traceparent formats the header for an outgoing request.
func (s *Span) Traceparent() string {
	f := "00"
	if s.Sampled {
		f = "01"
	}
	return "00-" + s.TraceID + "-" + s.SpanID + "-" + f
}

type ctxKey struct{}

// FromContext returns the current span, or nil.
func FromContext(ctx context.Context) *Span {
	s, _ := ctx.Value(ctxKey{}).(*Span)
	return s
}

func randFloat() float64 {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	return float64(n>>11) / float64(1<<53)
}

func (t *Tracer) newSpan(name string, kind int, traceID, parentID string, sampled bool) *Span {
	if traceID == "" {
		traceID = randHex(16)
		sampled = t.cfg.SampleRatio >= 1 || randFloat() < t.cfg.SampleRatio
	}
	return &Span{TraceID: traceID, SpanID: randHex(8), ParentID: parentID, Name: name, Start: time.Now(), Attrs: map[string]string{}, Sampled: sampled, kind: kind, tracer: t}
}

// Start begins a child span of the span in ctx (or a new trace when there is none).
// Call End on the result.
func (t *Tracer) Start(ctx context.Context, name string) (context.Context, *Span) {
	var s *Span
	if p := FromContext(ctx); p != nil {
		s = t.newSpan(name, 1, p.TraceID, p.SpanID, p.Sampled)
	} else {
		s = t.newSpan(name, 1, "", "", false)
	}
	return context.WithValue(ctx, ctxKey{}, s), s
}

// SetAttr sets a string attribute. Do not put secrets or personal data in attributes.
func (s *Span) SetAttr(k, v string) { s.mu.Lock(); s.Attrs[k] = v; s.mu.Unlock() }

// SetError marks the span as failed.
func (s *Span) SetError() { s.mu.Lock(); s.Error = true; s.mu.Unlock() }

// End finishes the span and queues it for export. Calling End twice is harmless.
func (s *Span) End() {
	s.mu.Lock()
	if !s.EndTime.IsZero() {
		s.mu.Unlock()
		return
	}
	s.EndTime = time.Now()
	s.mu.Unlock()
	s.tracer.enqueue(s)
}

// Middleware starts a server span per request and propagates the trace context.
func (t *Tracer) Middleware() aemeath.Middleware {
	return func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			traceID, parent, sampled, ok := ParseTraceparent(c.R.Header.Get("traceparent"))
			if !ok {
				traceID, parent, sampled = "", "", false
			}
			span := t.newSpan(c.R.Method, 2, traceID, parent, sampled)
			c.R = c.R.WithContext(context.WithValue(c.R.Context(), ctxKey{}, span))
			c.State["trace_id"] = span.TraceID
			sw := &statusCapture{ResponseWriter: c.W, status: 200}
			c.W = sw
			err := next(c)
			status := sw.status
			if err != nil && !sw.wrote {
				status = aemeath.AsHTTPError(err).Code
			}
			route := c.R.Pattern
			if _, rest, ok := strings.Cut(route, " "); ok {
				route = rest
			}
			if route == "" {
				span.Name = c.R.Method + " unmatched"
			} else {
				span.Name = c.R.Method + " " + route
				span.SetAttr("http.route", route)
			}
			span.SetAttr("http.request.method", c.R.Method)
			span.SetAttr("url.path", c.R.URL.Path) // never the query string
			span.SetAttr("http.response.status_code", strconv.Itoa(status))
			if status >= 500 {
				span.SetError()
			}
			span.End()
			return err
		}
	}
}

type statusCapture struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusCapture) WriteHeader(code int) {
	if !w.wrote {
		w.wrote, w.status = true, code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusCapture) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
func (w *statusCapture) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusCapture) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (t *Tracer) enqueue(s *Span) {
	if !s.Sampled || t.cfg.Endpoint == "" || t.closed.Load() {
		return
	}
	select {
	case t.queue <- s:
	default:
		t.dropped.Add(1) // never block a request on the exporter
	}
}

func (t *Tracer) loop() {
	defer t.wg.Done()
	tick := time.NewTicker(t.cfg.FlushInterval)
	defer tick.Stop()
	var batch []*Span
	flush := func() {
		if len(batch) > 0 {
			t.export(batch)
			batch = nil
		}
	}
	for {
		select {
		case s := <-t.queue:
			batch = append(batch, s)
			if len(batch) >= t.cfg.BatchSize {
				flush()
			}
		case <-tick.C:
			flush()
		case ack := <-t.flushCh:
			for drained := false; !drained; {
				select {
				case s := <-t.queue:
					batch = append(batch, s)
				default:
					drained = true
				}
			}
			flush()
			close(ack)
		case <-t.done:
			for drained := false; !drained; {
				select {
				case s := <-t.queue:
					batch = append(batch, s)
				default:
					drained = true
				}
			}
			flush()
			return
		}
	}
}

// Flush exports everything queued so far and waits for it (or ctx).
func (t *Tracer) Flush(ctx context.Context) error {
	ack := make(chan struct{})
	select {
	case t.flushCh <- ack:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown flushes pending spans and stops the exporter.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(t.done)
	fin := make(chan struct{})
	go func() { t.wg.Wait(); close(fin) }()
	select {
	case <-fin:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type kv struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

func str(k, v string) kv { return kv{k, map[string]any{"stringValue": v}} }

func (t *Tracer) payload(spans []*Span) ([]byte, error) {
	out := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		s.mu.Lock()
		keys := make([]string, 0, len(s.Attrs))
		for k := range s.Attrs {
			keys = append(keys, k)
		}
		sortStrings(keys)
		attrs := make([]kv, 0, len(keys))
		for _, k := range keys {
			attrs = append(attrs, str(k, s.Attrs[k]))
		}
		sp := map[string]any{
			"traceId": s.TraceID, "spanId": s.SpanID, "name": s.Name, "kind": s.kind,
			"startTimeUnixNano": strconv.FormatInt(s.Start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(s.EndTime.UnixNano(), 10),
			"attributes":        attrs,
		}
		if s.ParentID != "" {
			sp["parentSpanId"] = s.ParentID
		}
		if s.Error {
			sp["status"] = map[string]any{"code": 2}
		}
		s.mu.Unlock()
		out = append(out, sp)
	}
	return json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{
		"resource":   map[string]any{"attributes": []kv{str("service.name", t.cfg.ServiceName)}},
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "aemeath"}, "spans": out}},
	}}})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (t *Tracer) export(spans []*Span) {
	body, err := t.payload(spans)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.cfg.Client.Do(req)
	if err != nil {
		log.Printf("aemeath/tracing: export of %d spans failed: %v", len(spans), err)
		t.dropped.Add(uint64(len(spans)))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		log.Printf("aemeath/tracing: collector answered %s for %d spans", resp.Status, len(spans))
		t.dropped.Add(uint64(len(spans)))
	}
}
