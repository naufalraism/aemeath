package aemeath

import (
	"net/http"
	"reflect"
	"strings"
)

type Group struct {
	app    *App
	prefix string
	mws    []Middleware
}

func (a *App) Group(prefix string, mws ...Middleware) *Group {
	return &Group{
		app:    a,
		prefix: strings.TrimSuffix(prefix, "/"),
		mws:    mws,
	}
}

func (g *Group) Group(prefix string, mws ...Middleware) *Group {
	newPrefix := g.prefix + "/" + strings.TrimSuffix(strings.TrimPrefix(prefix, "/"), "/")
	return &Group{
		app:    g.app,
		prefix: newPrefix,
		mws:    append(g.mws, mws...),
	}
}

func (g *Group) Use(m ...Middleware) {
	g.mws = append(g.mws, m...)
}

func (g *Group) wrap(h HandlerFunc) HandlerFunc {
	for i := len(g.mws) - 1; i >= 0; i-- {
		h = g.mws[i](h)
	}
	return h
}

// GroupRoute mendaftarkan rute bertipe ke dalam Group.
func GroupRoute[In, Out any](g *Group, method, path string, fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	fullPath := g.prefix + "/" + strings.TrimPrefix(path, "/")
	if fullPath == "/" || fullPath == "" {
		fullPath = g.prefix
	}

	cfg := routeConfig{status: http.StatusOK}
	for _, o := range opts {
		o(&cfg)
	}
	validateInputType(reflect.TypeFor[In]())

	g.app.specs = append(g.app.specs, routeSpec{
		method: method, path: fullPath,
		in: reflect.TypeFor[In](), out: reflect.TypeFor[Out](),
		cfg: cfg,
	})

	handler := func(c *Ctx) error {
		var in In
		if err := bind(c, &in); err != nil {
			return err
		}
		out, err := fn(c, in)
		if err != nil {
			return err
		}
		return c.JSON(cfg.status, out)
	}

	wrapped := g.wrap(handler)
	g.app.Handle(method, fullPath, wrapped)

	return Endpoint[In, Out]{Method: method, Path: fullPath}
}

func GroupGET[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "GET", path, fn, o...)
}
func GroupPOST[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "POST", path, fn, o...)
}
func GroupPUT[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "PUT", path, fn, o...)
}
func GroupPATCH[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "PATCH", path, fn, o...)
}
func GroupDELETE[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "DELETE", path, fn, o...)
}

func RegisterGroup[In, Out any](g *Group, e Endpoint[In, Out], fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, e.Method, e.Path, fn, opts...)
}
