package aemeath

import (
	"fmt"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

// Group prefixes and route paths are joined into a pattern. Whatever the
// developer writes, the route must be reachable at the joined path, or
// registration must refuse it with a clear message.
func FuzzGroupPaths(f *testing.F) {
	for _, s := range [][2]string{{"/api", "/users"}, {"api", "users"}, {"/api/", "/"}, {"", "/"}, {"/", "/x"}, {"//", "/x"}, {"/api//", "/x"}, {"/a", "/b/"}, {"/v1/", "//x"}, {"a//", "/"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, prefix, route string) {
		for _, c := range prefix + route {
			if c != '/' && (c < 'a' || c > 'z') {
				t.Skip() // only the shape of the slashes is under test
			}
		}
		if len(prefix)+len(route) > 24 {
			t.Skip()
		}
		app := New()
		var perr any
		func() {
			defer func() { perr = recover() }()
			app.Group(prefix).Handle("GET", route, func(c *Ctx) error { return c.Text(200, "hit") })
		}()
		if perr != nil {
			if msg := fmt.Sprint(perr); strings.HasPrefix(msg, "aemeath:") || strings.Contains(msg, "pattern") {
				return // refused at startup with a message saying why
			}
			t.Fatalf("Group(%q).Handle(%q): panic: %v", prefix, route, perr)
		}
		// The path a reader of the code expects: prefix and route joined,
		// repeated slashes collapsed, a trailing slash of the route kept.
		want := path.Clean("/" + prefix + "/" + route)
		if strings.HasSuffix(route, "/") && strings.Trim(route, "/") != "" && want != "/" {
			want += "/"
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", want, nil))
		if rec.Code != 200 {
			t.Fatalf("Group(%q).Handle(\"GET\", %q): GET %s answers %d", prefix, route, want, rec.Code)
		}
	})
}
