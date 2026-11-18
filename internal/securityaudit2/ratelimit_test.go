package securityaudit2

import (
	"fmt"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

// Not a weakness that was fixed: a measurement. The limiter keeps one entry
// per client for one window and has no upper bound; 200,000 clients cost
// about 16 MB. This test fails if that grows a lot.
func TestRateLimiter_MemoryPerClient(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RateLimiter(10, time.Hour))
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { return nil })
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	const clients = 200_000
	for i := 0; i < clients; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		if i < 65536 { // distinct /64s inside one /48, which one hosting customer typically gets
			req.RemoteAddr = fmt.Sprintf("[2001:db8:1:%x::1]:1234", i)
		} else {
			req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1234", (i>>16)&255, (i>>8)&255, i&255)
		}
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
	runtime.GC()
	runtime.ReadMemStats(&ms1)
	runtime.KeepAlive(app)
	grew := int64(ms1.HeapAlloc) - int64(ms0.HeapAlloc)
	t.Logf("%d distinct clients within one window: heap grew by %d MB (%d bytes per client)", clients, grew>>20, grew/clients)
	if grew/clients > 512 {
		t.Errorf("%d bytes per client", grew/clients)
	}
}
