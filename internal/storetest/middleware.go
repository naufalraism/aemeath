package storetest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

// RunConcurrentRequests checks the session middleware on a store when
// several requests of one visitor overlap (two tabs, polling, an upload in
// progress). Each request used to save the whole session it loaded, so:
//
//   - a slow request erased what a faster one saved under another key;
//   - a slow request that finished after a logout stored the session again,
//     logging the user back in.
//
// Now a request saves only the keys it changed, on top of what is stored,
// and does not bring back a session that was deleted in the meantime.
func RunConcurrentRequests(t *testing.T, newStore func(t *testing.T) session.Store) {
	type harness struct {
		do      func(method, path string, ck *http.Cookie) *httptest.ResponseRecorder
		started chan struct{}
		release chan struct{}
		login   func() *http.Cookie
	}
	setup := func(t *testing.T, sliding bool) *harness {
		h := &harness{started: make(chan struct{}, 4), release: make(chan struct{})}
		app := aemeath.New()
		app.Use(session.Middleware(session.Config{Store: newStore(t), Insecure: true, Sliding: sliding}))
		app.Handle("POST", "/login", func(c *aemeath.Ctx) error {
			s := session.From(c)
			s.Set("user_id", "alice")
			s.Set("cart", "empty")
			return c.Text(200, "ok")
		})
		app.Handle("POST", "/set", func(c *aemeath.Ctx) error {
			s := session.From(c)
			if c.Query("slow") != "" {
				h.started <- struct{}{}
				<-h.release
			}
			if v := c.Query("v"); v != "" {
				s.Set(c.Query("k"), v)
			} else if c.Query("k") != "" {
				s.Delete(c.Query("k"))
			}
			return c.Text(200, "ok")
		})
		app.Handle("POST", "/logout", func(c *aemeath.Ctx) error { session.From(c).Destroy(); return c.Text(200, "bye") })
		app.Handle("GET", "/me", func(c *aemeath.Ctx) error {
			s := session.From(c)
			var b strings.Builder
			for _, k := range []string{"user_id", "cart", "theme"} {
				v, _ := s.Get(k)
				b.WriteString(k + "=")
				if v != nil {
					b.WriteString(v.(string))
				}
				b.WriteString(" ")
			}
			return c.Text(200, strings.TrimSpace(b.String()))
		})
		h.do = func(method, path string, ck *http.Cookie) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, nil)
			if ck != nil {
				r.AddCookie(ck)
			}
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			return w
		}
		h.login = func() *http.Cookie {
			cks := h.do("POST", "/login", nil).Result().Cookies()
			if len(cks) != 1 {
				t.Fatalf("login set %d cookies", len(cks))
			}
			return cks[0]
		}
		return h
	}
	// slowly runs path in the background and returns once its handler waits.
	slowly := func(h *harness, path string, ck *http.Cookie) <-chan *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- h.do("POST", path, ck) }()
		<-h.started
		return done
	}
	me := func(h *harness, ck *http.Cookie) string { return h.do("GET", "/me", ck).Body.String() }

	t.Run("changes to different keys are all kept", func(t *testing.T) {
		h := setup(t, false)
		ck := h.login()
		done := slowly(h, "/set?slow=1&k=cart&v=book", ck)
		h.do("POST", "/set?k=theme&v=dark", ck)
		close(h.release)
		<-done
		if got, want := me(h, ck), "user_id=alice cart=book theme=dark"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("a delete is kept while another key changes", func(t *testing.T) {
		h := setup(t, false)
		ck := h.login()
		done := slowly(h, "/set?slow=1&k=theme&v=dark", ck)
		h.do("POST", "/set?k=cart", ck) // deletes cart
		close(h.release)
		<-done
		if got, want := me(h, ck), "user_id=alice cart= theme=dark"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("the same key: the last save wins", func(t *testing.T) {
		h := setup(t, false)
		ck := h.login()
		done := slowly(h, "/set?slow=1&k=cart&v=slow", ck)
		h.do("POST", "/set?k=cart&v=fast", ck)
		close(h.release)
		<-done
		if got, want := me(h, ck), "user_id=alice cart=slow theme="; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	for _, sliding := range []bool{false, true} {
		name := "a slow request does not undo a logout"
		path := "/set?slow=1&k=cart&v=pen"
		if sliding {
			name += " (Sliding, read-only request)"
			path = "/set?slow=1"
		}
		t.Run(name, func(t *testing.T) {
			h := setup(t, sliding)
			ck := h.login()
			done := slowly(h, path, ck)
			h.do("POST", "/logout", ck)
			close(h.release)
			slow := <-done
			if got, want := me(h, ck), "user_id= cart= theme="; got != want {
				t.Errorf("after logout and the slow request: %q, want %q", got, want)
			}
			for _, c := range slow.Result().Cookies() {
				if c.Value != "" && c.MaxAge >= 0 {
					t.Errorf("the slow request set the session cookie again: %v", c)
				}
			}
		})
	}
}

// RunValueTypes checks what a handler reads back from the session on the
// next request: the same types with every store, and Int for any integer.
func RunValueTypes(t *testing.T, newStore func(t *testing.T) session.Store) {
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: newStore(t), Insecure: true}))
	app.Handle("POST", "/set", func(c *aemeath.Ctx) error {
		s := session.From(c)
		s.Set("count", 3)
		s.Set("uid", uint(42)) // a GORM primary key
		s.Set("big", int64(1)<<40)
		s.Set("neg", int8(-5))
		s.Set("half", 2.5)
		s.Set("huge", uint64(1)<<63) // does not fit in an int
		s.Set("name", "ann")
		// In the same request a value reads back as it was set.
		// Int reads every integer type as it was set, before any store.
		if s.Int("uid") != 42 || s.Int("neg") != -5 || s.Int("big") != 1<<40 {
			return c.Text(500, fmt.Sprintf("same request: Int = %d, %d, %d", s.Int("uid"), s.Int("neg"), s.Int("big")))
		}
		if s.Int("huge") != 0 {
			return c.Text(500, "same request: Int of 2^63 is not 0")
		}
		if v, _ := s.Get("count"); v != 3 {
			return c.Text(500, fmt.Sprintf("same request: count = %#v", v))
		}
		return c.Text(200, "ok")
	})
	app.Handle("GET", "/get", func(c *aemeath.Ctx) error {
		s := session.From(c)
		count, _ := s.Get("count")
		return c.JSON(200, map[string]any{
			"countType": fmt.Sprintf("%T", count),
			"count":     s.Int("count"), "uid": s.Int("uid"), "big": s.Int("big"),
			"neg": s.Int("neg"), "half": s.Int("half"), "huge": s.Int("huge"), "name": s.Int("name"), "missing": s.Int("missing"),
		})
	})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("POST", "/set", nil))
	if w.Code != 200 {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/get", nil)
	for _, ck := range w.Result().Cookies() {
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	want := `{"big":1099511627776,"count":3,"countType":"float64","half":0,"huge":0,"missing":0,"name":0,"neg":-5,"uid":42}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("next request read\n got %s\nwant %s", got, want)
	}
}
