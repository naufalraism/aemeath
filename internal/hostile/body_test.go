//go:build !windows

package hostile

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

// countingConn counts the bytes the client manages to send before the server
// stops reading (the kernel buffers some of it).
func sendForever(c net.Conn, chunk []byte, limit int64) (sent int64) {
	for sent < limit {
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Write(chunk)
		sent += int64(n)
		if err != nil {
			return sent
		}
	}
	return sent
}

// A body far larger than any limit, announced with Content-Length or sent
// chunked, to a JSON route, a form route and a route that reads the body
// itself.
func TestHugeBodies(t *testing.T) {
	var rawRead atomic.Int64
	app := aemeath.New()
	aemeath.POST(app, "/json", func(_ *aemeath.Ctx, in item) (item, error) { return in, nil })
	aemeath.POST(app, "/form", func(_ *aemeath.Ctx, _ struct {
		Name string `form:"name"`
	}) (struct{}, error) {
		return struct{}{}, nil
	})
	raw := func(c *aemeath.Ctx) error {
		n, err := io.Copy(io.Discard, c.R.Body) // a handler that reads the body itself
		rawRead.Store(n)
		if err != nil {
			return err // the error as is: the framework picks the status
		}
		return c.Text(200, fmt.Sprint(n))
	}
	app.Handle("POST", "/raw", raw)
	app.Group("/big", aemeath.MaxBodySize(64<<20)).Handle("POST", "/upload", raw) // a group that raised its cap
	addr := server(t, app, nil)
	chunk := []byte(strings.Repeat("a", 64<<10))
	for _, tc := range []struct{ name, path, head string }{
		{"JSON, Content-Length 10 GB", "/json", "Content-Type: application/json\r\nContent-Length: 10737418240\r\n"},
		{"JSON, chunked", "/json", "Content-Type: application/json\r\nTransfer-Encoding: chunked\r\n"},
		{"form, Content-Length 10 GB", "/form", "Content-Type: application/x-www-form-urlencoded\r\nContent-Length: 10737418240\r\n"},
		{"raw handler, 300 MB", "/raw", "Content-Length: 314572800\r\n"},
		{"raw handler, 300 MB chunked", "/raw", "Transfer-Encoding: chunked\r\n"},
		{"MaxBodySize(64 MB) group, 40 MB", "/big/upload", "Content-Length: 41943040\r\n"},
	} {
		c := dial(t, addr)
		_, _ = fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: x\r\n%s\r\n", tc.path, tc.head)
		start := time.Now()
		var sent int64
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := chunk
			if strings.Contains(tc.head, "chunked") {
				body = []byte(fmt.Sprintf("%x\r\n%s\r\n", len(chunk), chunk))
			}
			limit := int64(1 << 30)
			switch tc.path {
			case "/raw":
				limit = 300 << 20
			case "/big/upload":
				limit = 40 << 20
			}
			sent = sendForever(c, body, limit)
		}()
		rawRead.Store(-1)
		code, body, err := readResponse(c, 20*time.Second)
		took := time.Since(start)
		_ = c.Close()
		wg.Wait()
		t.Logf("%-32s -> %d %-60q after %v; client had sent %d MB, handler read %d MB", tc.name, code, strings.TrimSpace(body), took.Round(time.Millisecond), sent>>20, max(rawRead.Load(), 0)>>20)
		if err != nil {
			t.Errorf("%s: no response: %v", tc.name, err)
		}
		want := 413
		if tc.path == "/big/upload" {
			want = 200
		}
		if code != want {
			t.Errorf("%s: status %d, want %d", tc.name, code, want)
		}
		if tc.path == "/raw" && rawRead.Load() > 32<<20 {
			t.Errorf("%s: the handler read %d MB, past the default cap of 32 MB", tc.name, rawRead.Load()>>20)
		}
	}
}
