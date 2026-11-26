package metrics_test

import (
	"math"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath/metrics"
)

func panicOf(f func()) (msg string) {
	defer func() { msg, _ = recover().(string) }()
	f()
	return ""
}

// The text format allows each bound once and only numbers: a repeated bound
// or le="NaN" makes the page invalid for Prometheus.
func TestHistogramRefusesBoundsThePrometheusFormatCannotHold(t *testing.T) {
	for _, tc := range []struct {
		name    string
		buckets []float64
		want    string
	}{
		{"repeated bound", []float64{1, 0.5, 1}, "1 twice"},
		{"NaN", []float64{0.5, math.NaN()}, "NaN"},
	} {
		msg := panicOf(func() { metrics.New().Histogram("job_seconds", "x", tc.buckets) })
		if !strings.Contains(msg, "job_seconds") || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: recover() = %q, want a panic naming job_seconds and %q", tc.name, msg, tc.want)
		}
		msg = panicOf(func() { metrics.New(metrics.Config{DurationBuckets: tc.buckets}) })
		if !strings.Contains(msg, "http_request_duration_seconds") || !strings.Contains(msg, tc.want) {
			t.Errorf("%s in Config: recover() = %q, want a panic naming the duration histogram and %q", tc.name, msg, tc.want)
		}
	}
}

// +Inf is always the last bucket, so listing it again would print it twice.
func TestHistogramWritesPlusInfOnce(t *testing.T) {
	reg := metrics.New()
	h := reg.Histogram("job_seconds", "x", []float64{math.Inf(1), 1})
	h.Observe(0.5)
	out := reg.Render()
	if n := strings.Count(out, `job_seconds_bucket{le="+Inf"}`); n != 1 {
		t.Fatalf("le=\"+Inf\" written %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, `job_seconds_bucket{le="1"} 1`) {
		t.Fatalf("the finite bound is missing:\n%s", out)
	}
}
