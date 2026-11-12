// Package metrics exposes Prometheus metrics with no third-party dependency.
//
//	reg := metrics.New()
//	app.Use(reg.Middleware())
//	app.Handle("GET", "/metrics", reg.Handler())
//
// The middleware records http_requests_total, http_request_duration_seconds and
// http_requests_in_flight, labelled by the route PATTERN (never the raw path),
// so label cardinality stays bounded no matter what clients request.
package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/naufalraism/aemeath"
)

// DefaultBuckets are the histogram buckets (seconds) used for request durations.
var DefaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// maxSeries bounds the number of label combinations per metric. Extra
// combinations are folded into one "overflow" series instead of growing forever.
const maxSeries = 2000

// Registry holds metrics and renders them in the Prometheus text format.
type Registry struct {
	mu      sync.Mutex
	metrics []*family
	byName  map[string]*family

	requests *Counter
	duration *Histogram
	inflight *Gauge
}

type family struct {
	name, help, typ string
	labels          []string
	buckets         []float64
	mu              sync.Mutex
	series          map[string]*series
	order           []string
}

type series struct {
	values  []string
	val     float64
	buckets []uint64
	sum     float64
	count   uint64
}

// Counter only goes up.
type Counter struct{ f *family }

// Gauge goes up and down.
type Gauge struct{ f *family }

// Histogram observes values into buckets.
type Histogram struct{ f *family }

// New creates a Registry with the HTTP metrics already registered.
func New() *Registry {
	r := &Registry{byName: map[string]*family{}}
	r.requests = r.Counter("http_requests_total", "Total HTTP requests.", "method", "route", "status")
	r.duration = r.Histogram("http_request_duration_seconds", "HTTP request latency in seconds.", DefaultBuckets, "method", "route")
	r.inflight = r.Gauge("http_requests_in_flight", "HTTP requests currently being served.")
	return r
}

func (r *Registry) register(name, help, typ string, buckets []float64, labels []string) *family {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byName[name]; dup {
		panic("metrics: duplicate metric " + name)
	}
	f := &family{name: name, help: help, typ: typ, labels: labels, buckets: buckets, series: map[string]*series{}}
	r.byName[name] = f
	r.metrics = append(r.metrics, f)
	return f
}

// Counter registers a counter. It panics on a duplicate name.
func (r *Registry) Counter(name, help string, labels ...string) *Counter {
	return &Counter{r.register(name, help, "counter", nil, labels)}
}

// Gauge registers a gauge.
func (r *Registry) Gauge(name, help string, labels ...string) *Gauge {
	return &Gauge{r.register(name, help, "gauge", nil, labels)}
}

// Histogram registers a histogram with the given upper bounds (sorted ascending).
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *Histogram {
	b := append([]float64(nil), buckets...)
	sort.Float64s(b)
	return &Histogram{r.register(name, help, "histogram", b, labels)}
}

func (f *family) get(vals []string) *series {
	if len(vals) != len(f.labels) {
		panic(fmt.Sprintf("metrics: %s expects %d label values, got %d", f.name, len(f.labels), len(vals)))
	}
	key := strings.Join(vals, "\xff")
	s, ok := f.series[key]
	if ok {
		return s
	}
	if len(f.series) >= maxSeries {
		over := make([]string, len(vals))
		for i := range over {
			over[i] = "overflow"
		}
		key = strings.Join(over, "\xff")
		if s, ok = f.series[key]; ok {
			return s
		}
		vals = over
	}
	s = &series{values: append([]string(nil), vals...)}
	if f.typ == "histogram" {
		s.buckets = make([]uint64, len(f.buckets))
	}
	f.series[key] = s
	f.order = append(f.order, key)
	return s
}

// Inc adds 1.
func (c *Counter) Inc(labels ...string) { c.Add(1, labels...) }

// Add adds v (ignored when negative or NaN: counters never decrease).
func (c *Counter) Add(v float64, labels ...string) {
	if v < 0 || math.IsNaN(v) {
		return
	}
	c.f.mu.Lock()
	c.f.get(labels).val += v
	c.f.mu.Unlock()
}

// Set sets the value.
func (g *Gauge) Set(v float64, labels ...string) {
	g.f.mu.Lock()
	g.f.get(labels).val = v
	g.f.mu.Unlock()
}

// Add adds v (may be negative).
func (g *Gauge) Add(v float64, labels ...string) {
	g.f.mu.Lock()
	g.f.get(labels).val += v
	g.f.mu.Unlock()
}

// Observe records one value.
func (h *Histogram) Observe(v float64, labels ...string) {
	if math.IsNaN(v) {
		return
	}
	h.f.mu.Lock()
	s := h.f.get(labels)
	s.sum += v
	s.count++
	for i, ub := range h.f.buckets {
		if v <= ub {
			s.buckets[i]++
		}
	}
	h.f.mu.Unlock()
}

// Middleware records the HTTP metrics. Put it first so it sees every request.
func (r *Registry) Middleware() aemeath.Middleware {
	return func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			start := time.Now()
			r.inflight.Add(1)
			sw := &statusCapture{ResponseWriter: c.W, status: 200}
			c.W = sw
			err := next(c)
			r.inflight.Add(-1)
			status := sw.status
			if err != nil && !sw.wrote {
				status = aemeath.AsHTTPError(err).Code
			}
			route := RouteOf(c.R)
			r.requests.Inc(c.R.Method, route, strconv.Itoa(status))
			r.duration.Observe(time.Since(start).Seconds(), c.R.Method, route)
			return err
		}
	}
}

// RouteOf returns the matched route pattern without the method ("/users/{id}"),
// or "unmatched" when no route matched (so scanners cannot create series).
func RouteOf(r *http.Request) string {
	p := r.Pattern
	if p == "" {
		return "unmatched"
	}
	if _, rest, ok := strings.Cut(p, " "); ok {
		return rest
	}
	return p
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
	if !w.wrote {
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusCapture) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusCapture) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Handler serves the metrics in the Prometheus text format. It exposes route
// patterns and traffic volumes, so do not publish it to the internet:
// protect it or serve it on an internal port.
func (r *Registry) Handler() aemeath.HandlerFunc {
	return func(c *aemeath.Ctx) error {
		c.W.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, err := c.W.Write([]byte(r.Render()))
		return err
	}
}

func esc(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}

func labelStr(names, vals []string, extra ...string) string {
	var parts []string
	for i, n := range names {
		parts = append(parts, n+`="`+esc(vals[i])+`"`)
	}
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func fmtFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Render returns the exposition text.
func (r *Registry) Render() string {
	r.mu.Lock()
	fams := append([]*family(nil), r.metrics...)
	r.mu.Unlock()
	var b strings.Builder
	for _, f := range fams {
		f.mu.Lock()
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", f.name, strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(f.help), f.name, f.typ)
		if len(f.order) == 0 && len(f.labels) == 0 && f.typ != "histogram" {
			fmt.Fprintf(&b, "%s 0\n", f.name)
		}
		for _, k := range f.order {
			s := f.series[k]
			switch f.typ {
			case "histogram":
				for i, ub := range f.buckets {
					fmt.Fprintf(&b, "%s_bucket%s %d\n", f.name, labelStr(f.labels, s.values, `le="`+fmtFloat(ub)+`"`), s.buckets[i])
				}
				fmt.Fprintf(&b, "%s_bucket%s %d\n", f.name, labelStr(f.labels, s.values, `le="+Inf"`), s.count)
				fmt.Fprintf(&b, "%s_sum%s %s\n", f.name, labelStr(f.labels, s.values), fmtFloat(s.sum))
				fmt.Fprintf(&b, "%s_count%s %d\n", f.name, labelStr(f.labels, s.values), s.count)
			default:
				fmt.Fprintf(&b, "%s%s %s\n", f.name, labelStr(f.labels, s.values), fmtFloat(s.val))
			}
		}
		f.mu.Unlock()
	}
	return b.String()
}
