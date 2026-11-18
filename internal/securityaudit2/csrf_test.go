package securityaudit2

import (
	"bytes"
	"crypto/rand"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/csrf"
	"github.com/naufalraism/aemeath/session"
)

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// Before checking that the visitor had a session (and therefore a token), the
// middleware parsed a multipart body of up to 32 MiB, writing what did not fit
// in 8 MiB of memory to temporary files, on any POST route.
func TestCSRF_NoBodyIsReadWithoutASession(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}), csrf.Middleware(csrf.Config{}))
	aemeath.POST(app, "/api/items", func(_ *aemeath.Ctx, _ ampItem) (struct{}, error) { return struct{}{}, nil })

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, _ := w.CreateFormFile("f", "big.bin")
	chunk := make([]byte, 1<<20)
	_, _ = rand.Read(chunk)
	for i := 0; i < 20; i++ {
		_, _ = fw.Write(chunk)
	}
	_ = w.Close()

	var read, tmpBytes atomic.Int64
	req := httptest.NewRequest("POST", "/api/items", countingReader{&body, &read})
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.ServeHTTP(rec, req)
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			var sum int64
			_ = filepath.Walk(tmp, func(_ string, fi os.FileInfo, err error) error {
				if err == nil && !fi.IsDir() {
					sum += fi.Size()
				}
				return nil
			})
			if sum > tmpBytes.Load() {
				tmpBytes.Store(sum)
			}
			time.Sleep(time.Millisecond)
		}
	}
	t.Logf("no session cookie, no token: status %d after reading %d KB of body; up to %d KB in temporary files",
		rec.Code, read.Load()>>10, tmpBytes.Load()>>10)
	if rec.Code != 403 {
		t.Errorf("status %d, want 403", rec.Code)
	}
	if read.Load() > 64<<10 || tmpBytes.Load() > 0 {
		t.Errorf("a request that cannot carry a valid token (no session) made the server read %d KB", read.Load()>>10)
	}
}
