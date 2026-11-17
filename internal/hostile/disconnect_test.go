//go:build !windows

package hostile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

type item struct {
	Name string `json:"name"`
}

// The client sends the headers and part of a JSON body, then disconnects.
func TestDisconnectDuringUpload(t *testing.T) {
	l := captureLogs(t)
	var calls atomic.Int32
	app := aemeath.New()
	app.Use(aemeath.Logger())
	aemeath.POST(app, "/items", func(_ *aemeath.Ctx, in item) (item, error) {
		calls.Add(1)
		return in, nil
	})
	addr := server(t, app, nil)
	before := runtime.NumGoroutine()

	c := dial(t, addr)
	_, _ = fmt.Fprintf(c, "POST /items HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100000\r\n\r\n")
	_, _ = c.Write([]byte(`{"name":"` + strings.Repeat("a", 1000)))
	time.Sleep(50 * time.Millisecond)
	_ = c.Close()
	time.Sleep(300 * time.Millisecond)

	logged := strings.TrimSpace(l.String())
	left := settle(before, 2*time.Second)
	t.Logf("handler called: %d; log: %q; goroutines %d -> %d", calls.Load(), logged, before, left)
	if calls.Load() != 0 {
		t.Error("the handler ran with a body that never arrived")
	}
	if strings.Contains(logged, " 500 ") || strings.Contains(logged, "internal error") {
		t.Errorf("a client that left is logged as a server error: %s", logged)
	}
	if left > before {
		t.Errorf("goroutines left: %d (before %d)", left, before)
	}
}

// The handler is working (waiting on its context) when the client leaves.
func TestDisconnectWhileHandlerWorks(t *testing.T) {
	l := captureLogs(t)
	cancelled := make(chan time.Duration, 1)
	app := aemeath.New()
	app.Use(aemeath.Logger())
	app.Handle("GET", "/slow", func(c *aemeath.Ctx) error {
		start := time.Now()
		select {
		case <-c.Context().Done():
			cancelled <- time.Since(start)
			return c.Context().Err() // what a handler gets from a database call
		case <-time.After(5 * time.Second):
			return c.Text(200, "done")
		}
	})
	addr := server(t, app, nil)

	c := dial(t, addr)
	_, _ = fmt.Fprintf(c, "GET /slow HTTP/1.1\r\nHost: x\r\n\r\n")
	time.Sleep(100 * time.Millisecond)
	closedAt := time.Now()
	_ = c.Close()
	var after time.Duration
	select {
	case <-cancelled:
		after = time.Since(closedAt)
	case <-time.After(3 * time.Second):
		t.Fatal("the handler's context was not cancelled within 3 s of the client leaving")
	}
	time.Sleep(100 * time.Millisecond)
	logged := strings.TrimSpace(l.String())
	t.Logf("context cancelled %v after the client closed; log: %q", after.Round(time.Millisecond), logged)
	if strings.Contains(logged, " 500 ") || strings.Contains(logged, "internal error") {
		t.Errorf("a client that left is logged and counted as a 500 server error: %s", logged)
	}
}

// A stream (SSE) whose client leaves: the handler must stop.
func TestDisconnectDuringStream(t *testing.T) {
	ended := make(chan error, 1)
	app := aemeath.New()
	app.Handle("GET", "/events", func(c *aemeath.Ctx) error {
		s, err := c.SSE()
		if err != nil {
			return err
		}
		for i := 0; ; i++ {
			if err := s.Send("tick", i); err != nil {
				ended <- err
				return nil
			}
			select {
			case <-c.Context().Done():
				ended <- c.Context().Err()
				return nil
			case <-time.After(10 * time.Millisecond):
			}
		}
	})
	addr := server(t, app, nil)
	before := runtime.NumGoroutine()
	c := dial(t, addr)
	_, _ = fmt.Fprintf(c, "GET /events HTTP/1.1\r\nHost: x\r\n\r\n")
	buf := make([]byte, 512)
	_, _ = c.Read(buf)
	closedAt := time.Now()
	_ = c.Close()
	select {
	case err := <-ended:
		t.Logf("stream handler stopped %v after the client left (%v)", time.Since(closedAt).Round(time.Millisecond), err)
	case <-time.After(3 * time.Second):
		t.Fatal("the stream handler kept running after its client left")
	}
	if left := settle(before, 2*time.Second); left > before {
		t.Errorf("goroutines left: %d (before %d)", left, before)
	}
}

// A large download whose client leaves after the first megabyte.
func TestDisconnectDuringDownload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(200 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	done := make(chan struct{})
	app := aemeath.New()
	app.Handle("GET", "/file", func(c *aemeath.Ctx) error {
		defer close(done)
		return c.File(path)
	})
	addr := server(t, app, nil)
	c := dial(t, addr)
	_, _ = fmt.Fprintf(c, "GET /file HTTP/1.1\r\nHost: x\r\n\r\n")
	n, _ := io.CopyN(io.Discard, c, 1<<20)
	closedAt := time.Now()
	_ = c.Close()
	select {
	case <-done:
		t.Logf("read %d MB of 200 MB, closed: the handler returned %v later", n>>20, time.Since(closedAt).Round(time.Millisecond))
	case <-time.After(5 * time.Second):
		t.Fatal("the download kept going after its client left")
	}
}

// The handler starts work that outlives the request with c.Context(): it is
// cancelled as soon as the response is sent. (Standard net/http behaviour;
// recorded here because it surprises people.)
func TestContextOutlivingTheRequest(t *testing.T) {
	result := make(chan error, 1)
	app := aemeath.New()
	app.Handle("POST", "/later", func(c *aemeath.Ctx) error {
		ctx := c.Context()
		go func() {
			select {
			case <-time.After(200 * time.Millisecond):
				result <- nil
			case <-ctx.Done():
				result <- ctx.Err()
			}
		}()
		return c.Text(202, "accepted")
	})
	addr := server(t, app, nil)
	c := dial(t, addr)
	_, _ = fmt.Fprintf(c, "POST /later HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n")
	code, _, _ := readResponse(c, 2*time.Second)
	err := <-result
	t.Logf("response %d; background work with c.Context(): %v (use context.WithoutCancel or the jobs package)", code, err)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected the request context to be cancelled after the response, got %v", err)
	}
}

// Timeout middleware: a handler that honors the context gets 504; one that
// ignores it runs to the end anyway.
func TestTimeoutMiddleware(t *testing.T) {
	var ignoringFinished atomic.Bool
	app := aemeath.New()
	app.Use(aemeath.Timeout(100 * time.Millisecond))
	app.Handle("GET", "/honors", func(c *aemeath.Ctx) error {
		<-c.Context().Done()
		return c.Context().Err()
	})
	app.Handle("GET", "/ignores", func(c *aemeath.Ctx) error {
		time.Sleep(400 * time.Millisecond)
		ignoringFinished.Store(true)
		return c.Text(200, "late")
	})
	addr := server(t, app, nil)
	for _, p := range []string{"/honors", "/ignores"} {
		c := dial(t, addr)
		start := time.Now()
		_, _ = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: x\r\n\r\n", p)
		code, body, _ := readResponse(c, 3*time.Second)
		t.Logf("%-9s -> %d %q after %v", p, code, strings.TrimSpace(body), time.Since(start).Round(10*time.Millisecond))
	}
	if !ignoringFinished.Load() {
		t.Error("expected the handler that ignores its context to run to the end")
	}
}
