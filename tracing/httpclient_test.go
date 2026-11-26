package tracing_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath/tracing"
)

type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

// The field has the name it has in oidc.Config and oauth2.Config.
func TestExportUsesConfigHTTPClient(t *testing.T) {
	col := newCollector(t, 200)
	tr := &countingTransport{}
	tracer := tracing.New(tracing.Config{ServiceName: "svc", Endpoint: col.srv.URL, FlushInterval: time.Hour,
		HTTPClient: &http.Client{Transport: tr}})
	get(newApp(tracer), "/users/1", nil)
	if err := tracer.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tr.n.Load() == 0 || len(col.spans()) == 0 {
		t.Fatalf("export went through the configured client %d time(s) and delivered %d span(s), want at least once each", tr.n.Load(), len(col.spans()))
	}
}
