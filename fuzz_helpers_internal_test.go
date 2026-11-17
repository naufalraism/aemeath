package aemeath

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
)

// registerRuntimeRoute registers a typed route whose input and output types
// were built at run time (reflect.StructOf), which the generic GET/POST
// cannot take. It does what Route does: the startup checks of the input, the
// spec the generators read, and a handler that binds and validates the input
// and answers with the zero value of the output (or the bound input, when
// echo is set and the types are the same).
func registerRuntimeRoute(a *App, method, path string, in, out reflect.Type) {
	info := prepareInput(in, method, path)
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

func doJSON(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
