package aemeath

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type routeID struct {
	ID int `path:"id"`
}

type routeFile struct {
	Path string `path:"path"`
}

type routeEcho struct {
	Pattern string            `json:"pattern"`
	Values  map[string]string `json:"values"`
}

func routingApp() (*App, *http.ServeMux) {
	app := New()
	mirror := http.NewServeMux()
	both := func(method, pattern string, values ...string) {
		echo := func(r *http.Request) routeEcho {
			e := routeEcho{Pattern: r.Pattern, Values: map[string]string{}}
			for _, v := range values {
				e.Values[v] = r.PathValue(v)
			}
			return e
		}
		mirror.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
			_ = writeJSONForTest(w, echo(r))
		})
		app.Handle(method, pattern, func(c *Ctx) error { return c.JSON(200, echo(c.R)) })
	}
	both("GET", "/{$}")
	both("GET", "/users/{id}", "id")
	both("POST", "/users")
	both("GET", "/files/{path...}", "path")
	both("DELETE", "/files/{path...}", "path")
	both("GET", "/a/b/")
	both("PUT", "/x/{a}/y/{b}", "a", "b")
	return app, mirror
}

// For any request line a server would accept, the app must answer like the
// standard ServeMux it is built on: the same route, the same path values,
// redirects where the mux redirects, 404 and 405 where it has no route; and
// never panic or 500.
func FuzzRouting(f *testing.F) {
	for _, s := range []string{
		"GET /", "GET /users/12", "GET /users/", "POST /users", "PUT /users", "GET /files/a/b%2Fc",
		"DELETE /files/", "GET /a/b", "GET /a/b/c", "HEAD /users/1", "OPTIONS /users", "GET //users/1",
		"GET /users/../files/x", "GET /x/1/y/2", "PATCH /x/1/y/2", "BREW /", "GET *", "CONNECT /users/1",
		"GET /%2e%2e/etc", "GET /users/%31", "GET http://other/users/1", "GET /files/%00",
	} {
		f.Add(s)
	}
	app, mirror := routingApp()
	f.Fuzz(func(t *testing.T, line string) {
		if strings.ContainsAny(line, "\r\n") {
			t.Skip()
		}
		raw := line + " HTTP/1.1\r\nHost: example.com\r\n\r\n"
		req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
		if err != nil {
			t.Skip() // the server itself answers 400
		}
		req2, _ := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))

		got := httptest.NewRecorder()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%q: panic: %v", line, r)
				}
			}()
			app.ServeHTTP(got, req)
		}()
		want := httptest.NewRecorder()
		mirror.ServeHTTP(want, req2)

		if got.Code >= 500 {
			t.Fatalf("%q: status %d %s", line, got.Code, got.Body.String())
		}
		if got.Code == 204 && req.Method == "OPTIONS" && want.Code == 405 {
			return // a plain OPTIONS on a known path: documented 204
		}
		if got.Code == 404 && want.Code == 301 {
			// The mux cleans the path with a redirect even when the clean
			// path has no route; the app answers 404 at once. Same outcome.
			loc, _, _ := strings.Cut(want.Header().Get("Location"), "?") // routing looks at the path only
			if r3, err := http.NewRequest(req.Method, loc, nil); err == nil {
				r3.RequestURI = loc
				after := httptest.NewRecorder()
				mirror.ServeHTTP(after, r3)
				if after.Code == 404 || after.Code == 405 || after.Code == 301 {
					return
				}
			}
		}
		if got.Code != want.Code {
			t.Fatalf("%q: app %d %q, net/http.ServeMux %d %q", line, got.Code, got.Body.String(), want.Code, want.Body.String())
		}
		switch {
		case got.Code == 200 && req.Method != "HEAD":
			if got.Body.String() != want.Body.String() {
				t.Fatalf("%q: app matched %s, ServeMux %s", line, got.Body.String(), want.Body.String())
			}
		case got.Code == 405:
			if got.Header().Get("Allow") != want.Header().Get("Allow") {
				t.Fatalf("%q: Allow %q, ServeMux %q", line, got.Header().Get("Allow"), want.Header().Get("Allow"))
			}
		case got.Code == 301 || got.Code == 307 || got.Code == 308:
			if got.Header().Get("Location") != want.Header().Get("Location") {
				t.Fatalf("%q: Location %q, ServeMux %q", line, got.Header().Get("Location"), want.Header().Get("Location"))
			}
		}
	})
}

// Typed path parameters: whatever the client puts in the URL, binding answers
// 200 with the value net/http decoded, or 400; never 500.
func FuzzTypedPathParams(f *testing.F) {
	for _, s := range []string{"1", "-1", "9223372036854775807", "9223372036854775808", "0x10", "1e3", "%31", "", "%00", "a%2Fb", "%ff"} {
		f.Add(s)
	}
	app := New()
	GET(app, "/n/{id}", func(_ *Ctx, in routeID) (routeID, error) { return in, nil })
	GET(app, "/f/{path...}", func(_ *Ctx, in routeFile) (routeFile, error) { return in, nil })
	f.Fuzz(func(t *testing.T, seg string) {
		for _, prefix := range []string{"/n/", "/f/"} {
			raw := "GET " + prefix + seg + " HTTP/1.1\r\nHost: x\r\n\r\n"
			req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
			if err != nil || strings.ContainsAny(seg, "\r\n") {
				continue
			}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code >= 500 {
				t.Fatalf("%s%s: %d %s", prefix, seg, rec.Code, rec.Body.String())
			}
			if prefix == "/f/" && rec.Code == 200 {
				var got routeFile
				_ = jsonUnmarshalForTest(rec.Body.Bytes(), &got)
				// The response is JSON, which turns each invalid byte into U+FFFD.
				var want string
				b, _ := json.Marshal(req.PathValue("path"))
				_ = json.Unmarshal(b, &want)
				if got.Path != want && req.Pattern != "" {
					t.Fatalf("%s%s: bound %q, net/http decoded %q", prefix, seg, got.Path, req.PathValue("path"))
				}
			}
		}
	})
}
