package tracing_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/naufalraism/aemeath/tracing"
)

// A span is shared between a handler and the goroutines it starts, and the
// exporter reads it after End. Its state changes only through methods that
// lock (SetAttr, SetError, End) and is read through Sampled and
// Traceparent; the fields that change are not exported, so code cannot
// write span.Attrs[k] behind the lock, which the race detector caught
// against the exporter (tracing.go, payload) before.
func TestSpanIsSafeForConcurrentUse(t *testing.T) {
	col := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer col.Close()
	tr := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: col.URL, SampleRatio: 1, BatchSize: 1, FlushInterval: time.Millisecond})
	for i := 0; i < 50; i++ {
		_, span := tr.Start(context.Background(), "work")
		if !span.Sampled() {
			t.Fatal("SampleRatio 1: span not sampled")
		}
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					span.SetAttr(fmt.Sprint(g, j), "v")
					span.SetError()
					_ = span.Traceparent()
					_ = span.Sampled()
					if j == 25 {
						span.End() // ending while others still write is harmless
					}
				}
			}()
		}
		wg.Wait()
	}
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
