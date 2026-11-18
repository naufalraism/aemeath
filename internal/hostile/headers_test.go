//go:build !windows

package hostile

import (
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

// Headers: one very long header, thousands of small ones, and many clients
// each sending close to the limit at once.
func TestHugeHeaders(t *testing.T) {
	app := aemeath.New()
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { return c.Text(200, fmt.Sprint(len(c.R.Header))) })
	addr := server(t, app, nil)
	try := func(name, headers string) int {
		c := dial(t, addr)
		start := time.Now()
		go func() { _, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n"+headers+"\r\n") }()
		code, body, err := readResponse(c, 10*time.Second)
		t.Logf("%-38s -> %d %q (%v) after %v", name, code, strings.TrimSpace(body), err, time.Since(start).Round(time.Millisecond))
		return code
	}
	if code := try("one 2 MB header", "X-Big: "+strings.Repeat("a", 2<<20)+"\r\n"); code != 431 {
		t.Errorf("a 2 MB header got %d, want 431", code)
	}
	if code := try("one 100 KB header", "X-Big: "+strings.Repeat("a", 100<<10)+"\r\n"); code != 431 {
		t.Errorf("a 100 KB header got %d, want 431 (limit %d KB)", code, aemeath.DefaultMaxHeaderBytes>>10)
	}
	var many strings.Builder
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&many, "X-H%d: v\r\n", i)
	}
	if code := try("10,000 headers (~120 KB)", many.String()); code != 431 {
		t.Errorf("10,000 headers got %d, want 431", code)
	}
	if code := try("one 48 KB header (under the limit)", "X-Big: "+strings.Repeat("a", 48<<10)+"\r\n"); code != 200 {
		t.Errorf("a 48 KB header got %d, want 200", code)
	}

	// 200 clients at once, each sending 1000 KB of headers: what it costs.
	runtime.GC()
	before := heapMB()
	var wg sync.WaitGroup
	var ok, refused atomic.Int32
	var peak atomic.Int64
	var failMu sync.Mutex
	fails := map[string]int{}
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				if h := int64(heapMB()); h > peak.Load() {
					peak.Store(h)
				}
			}
		}
	}()
	// One shared buffer: io.WriteString on a net.Conn would copy 1 MB per client
	// and the heap would measure the test, not the server.
	big := []byte("GET / HTTP/1.1\r\nHost: x\r\nX-Big: " + strings.Repeat("a", 1000<<10) + "\r\n\r\n")
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.Dial("tcp", addr)
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			start := time.Now()
			_, werr := c.Write(big)
			code, _, err := readResponse(c, 20*time.Second)
			if code == 200 {
				ok.Add(1)
				return
			}
			if code == 431 {
				refused.Add(1)
				return
			}
			why := fmt.Sprintf("status %d", code)
			if err != nil {
				why = err.Error()
				if i := strings.LastIndex(why, ": "); i >= 0 {
					why = why[i+2:]
				}
			}
			if werr != nil {
				why = "write: " + why
			}
			failMu.Lock()
			fails[fmt.Sprintf("%s (sending took %v)", why, time.Since(start).Round(time.Second))]++
			failMu.Unlock()
		}()
	}
	wg.Wait()
	close(stop)
	t.Logf("200 clients x 1000 KB of headers: %d answered 200; heap %.0f MB before, peak about %d MB (limit %d KB per request)", ok.Load(), before, peak.Load(), aemeath.DefaultMaxHeaderBytes>>10)
	t.Logf("  %3d refused with 431", refused.Load())
	for why, n := range fails {
		t.Logf("  %3d without a status: %s", n, why)
	}
	if ok.Load() != 0 {
		t.Errorf("%d requests with 1000 KB of headers were served", ok.Load())
	}
	if peak.Load() > 40 {
		t.Errorf("heap peaked at about %d MB for 200 clients", peak.Load())
	}
}

func heapMB() float64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapInuse) / (1 << 20)
}
