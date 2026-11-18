package session_test

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

// When the memory store is full, a new visitor gets no session and the
// handler's response, not a 500: the handler has already done its work, and
// a 500 made clients send it again. A flood logs one line, not one per request.
func TestFullStoreGivesNoSessionNot500(t *testing.T) {
	var buf bytes.Buffer
	defer log.SetOutput(log.Writer()) // restore: a nil writer makes every later log.Printf panic
	log.SetOutput(&buf)

	store := session.NewMemoryStore().WithMaxSessions(1)
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: store, Insecure: true}))
	var work atomic.Int64
	app.Handle("POST", "/visit", func(c *aemeath.Ctx) error {
		work.Add(1)
		session.From(c).Set("seen", true)
		return c.Text(201, "done")
	})
	visit := func(cookie string) (int, string, string) {
		req := httptest.NewRequest("POST", "/visit", nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String(), rec.Header().Get("Set-Cookie")
	}

	code, _, setCookie := visit("")
	if code != 201 || setCookie == "" {
		t.Fatalf("first visitor: %d, cookie %q", code, setCookie)
	}
	first := strings.SplitN(setCookie, ";", 2)[0]

	for i := 0; i < 50; i++ {
		code, body, c := visit("")
		if code != 201 || body != "done" || c != "" {
			t.Fatalf("visitor %d with the store full: %d %q, Set-Cookie %q; want the handler's 201 and no cookie", i+2, code, body, c)
		}
	}
	if code, _, _ := visit(first); code != 201 {
		t.Errorf("the existing session stopped working: %d", code)
	}
	if store.Len() != 1 {
		t.Errorf("store holds %d sessions, cap 1", store.Len())
	}
	lines := strings.Count(buf.String(), "memory store is full")
	t.Logf("52 requests, work done %d times, log lines about the full store: %d\n%s", work.Load(), lines, strings.TrimSpace(buf.String()))
	if lines != 1 {
		t.Errorf("%d log lines for one flood, want 1", lines)
	}
}
