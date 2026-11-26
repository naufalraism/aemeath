package metrics_test

import (
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/metrics"
)

// durationBounds returns the le="..." values of http_request_duration_seconds
// after one request, in the order they are rendered.
func durationBounds(t *testing.T, reg *metrics.Registry) []string {
	t.Helper()
	app := aemeath.New()
	app.Use(reg.Middleware())
	app.Handle("GET", "/metrics", reg.Handler())
	hit(app, "GET", "/metrics")
	var les []string
	for _, line := range strings.Split(hit(app, "GET", "/metrics").Body.String(), "\n") {
		if !strings.HasPrefix(line, "http_request_duration_seconds_bucket{") || !strings.Contains(line, `route="/metrics"`) {
			continue
		}
		i := strings.Index(line, `le="`) + len(`le="`)
		les = append(les, line[i:i+strings.Index(line[i:], `"`)])
	}
	return les
}

func TestDurationBucketsAreSetPerRegistry(t *testing.T) {
	custom := []float64{1, 0.1}
	reg := metrics.New(metrics.Config{DurationBuckets: custom})
	custom[0] = 99 // the registry keeps its own copy

	if got, want := strings.Join(durationBounds(t, reg), " "), "0.1 1 +Inf"; got != want {
		t.Fatalf("custom registry buckets = %q, want %q", got, want)
	}
	def := strings.Join(durationBounds(t, metrics.New()), " ")
	if want := "0.005 0.01 0.025 0.05 0.1 0.25 0.5 1 2.5 5 10 +Inf"; def != want {
		t.Fatalf("a second registry with no Config has buckets %q, want the defaults %q", def, want)
	}
}

func TestDefaultBucketsCannotBeChangedFromOutside(t *testing.T) {
	b := metrics.DefaultBuckets()
	for i := range b {
		b[i] = 42
	}
	got := strings.Join(durationBounds(t, metrics.New()), " ")
	if want := "0.005 0.01 0.025 0.05 0.1 0.25 0.5 1 2.5 5 10 +Inf"; got != want {
		t.Fatalf("after a caller changed DefaultBuckets(), New() uses %q, want %q", got, want)
	}
	if again := metrics.DefaultBuckets(); again[0] != 0.005 {
		t.Fatalf("DefaultBuckets()[0] = %v after a caller changed its copy", again[0])
	}
}

func TestNewTakesOneConfig(t *testing.T) {
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "one Config") {
			t.Fatalf("New with two Configs: recover() = %q, want a panic that says it takes one Config", msg)
		}
	}()
	metrics.New(metrics.Config{}, metrics.Config{DurationBuckets: []float64{1}})
}
