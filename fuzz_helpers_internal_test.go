package aemeath

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// registerRuntimeRoute registers a typed route whose input and output types
// were built at run time (reflect.StructOf), which the generic GET/POST
// cannot take. It does what Route does: the startup checks of the input, the
// spec the generators read, and a handler that binds and validates the input
// and answers with the zero value of the output (or the bound input, when
// echo is set and the types are the same).
func registerRuntimeRoute(a *App, method, path string, in, out reflect.Type) {
	info := prepareInput(in, method, path)
	checkJSONNames(out, map[reflect.Type]bool{}) // as Route does
	cfg := newRouteConfig(nil)
	a.specs = append(a.specs, routeSpec{method: method, path: path, in: in, out: out, cfg: cfg})
	a.Handle(method, path, func(c *Ctx) error {
		v := reflect.New(in)
		defer c.runCleanups()
		if err := bind(c, v.Interface(), info); err != nil {
			return err
		}
		if in == out {
			return c.JSON(http.StatusOK, v.Elem().Interface())
		}
		return c.JSON(http.StatusOK, reflect.New(out).Elem().Interface())
	})
}

func jsonMarshalForTest(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshalForTest(b []byte, v any) error { return json.Unmarshal(b, v) }

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

func fileExistsForTest(p string) bool { _, err := os.Stat(p); return err == nil }

func doJSON(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func writeJSONForTest(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

// skipNode skips a test that needs node (and Zod), unless
// AEMEATH_REQUIRE_NODE is set, as in CI, where a skip would hide a failure.
func skipNode(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("AEMEATH_REQUIRE_NODE") != "" {
		t.Fatal(why)
	}
	t.Skip(why)
}
