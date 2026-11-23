package storetest

import (
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
