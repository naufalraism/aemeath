package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/metrics"
)

type none struct{}
type idIn struct {
	ID string `path:"id"`
}

func setup() (*aemeath.App, *metrics.Registry) {
	reg := metrics.New()
	app := aemeath.New()
	app.Use(reg.Middleware())
	app.Handle("GET", "/metrics", reg.Handler())
	aemeath.GET(app, "/users/{id}", func(c *aemeath.Ctx, in idIn) (map[string]string, error) { return map[string]string{"id": in.ID}, nil })
	aemeath.GET(app, "/boom", func(c *aemeath.Ctx, _ none) (none, error) { return none{}, aemeath.NotFound("nope") })
	return app, reg
}

func hit(app *aemeath.App, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestRecordsRoutePatternAndStatus(t *testing.T) {
	app, _ := setup()
	hit(app, "GET", "/users/1")
	hit(app, "GET", "/users/2")
	hit(app, "GET", "/boom")
	out := hit(app, "GET", "/metrics").Body.String()
	for _, want := range []string{
		`http_requests_total{method="GET",route="/users/{id}",status="200"} 2`,
		`http_requests_total{method="GET",route="/boom",status="404"} 1`,
		`http_request_duration_seconds_count{method="GET",route="/users/{id}"} 2`,
		`http_request_duration_seconds_bucket{method="GET",route="/users/{id}",le="+Inf"} 2`,
		"# TYPE http_requests_total counter",
		"# TYPE http_request_duration_seconds histogram",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/users/1") || strings.Contains(out, "/users/2") {
		t.Error("raw paths leaked into labels (unbounded cardinality)")
	}
}

func TestUnmatchedRequestsShareOneSeries(t *testing.T) {
	app, _ := setup()
	for _, p := range []string{"/a", "/b/c", "/wp-admin", "/.env"} {
		hit(app, "GET", p)
	}
	out := hit(app, "GET", "/metrics").Body.String()
	if !strings.Contains(out, `route="unmatched",status="404"} 4`) {
		t.Fatalf("scanners must collapse into one series:\n%s", out)
	}
	for _, p := range []string{"wp-admin", ".env"} {
		if strings.Contains(out, p) {
			t.Fatalf("%s leaked", p)
		}
	}
}

func TestCustomMetricsAndEscaping(t *testing.T) {
	reg := metrics.New()
	c := reg.Counter("jobs_total", "Jobs.", "kind")
	c.Inc(`we"ird\` + "\n")
	c.Add(-5, "x") // ignored
	g := reg.Gauge("queue_depth", "Depth.")
	g.Set(3)
	h := reg.Histogram("job_seconds", "Job time.", []float64{1, 5})
	h.Observe(0.5)
	h.Observe(3)
	h.Observe(9)
	out := reg.Render()
	for _, want := range []string{
		`jobs_total{kind="we\"ird\\\n"} 1`,
		"queue_depth 3",
		`job_seconds_bucket{le="1"} 1`,
		`job_seconds_bucket{le="5"} 2`,
		`job_seconds_bucket{le="+Inf"} 3`,
		"job_seconds_sum 12.5",
		"job_seconds_count 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `kind="x"`) {
		t.Error("negative counter add must be ignored")
	}
}

func TestSeriesAreBounded(t *testing.T) {
	reg := metrics.New()
	c := reg.Counter("x_total", "x", "id")
	for i := 0; i < 5000; i++ {
		c.Inc(strings.Repeat("a", 1) + string(rune('A'+i%26)) + http.StatusText(200) + string(rune(i)))
	}
	if n := strings.Count(reg.Render(), "x_total{"); n > 2001 {
		t.Fatalf("series not bounded: %d", n)
	}
	if !strings.Contains(reg.Render(), `id="overflow"`) {
		t.Fatal("expected an overflow series")
	}
}

func TestDuplicateNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	reg := metrics.New()
	reg.Counter("http_requests_total", "dup")
}
