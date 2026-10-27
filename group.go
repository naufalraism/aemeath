package aemeath

import (
	"reflect"
	"strings"
	"sync"
)

// Group is a set of routes that share a path prefix and middlewares.
//
// Register a group's middlewares before creating sub-groups: a sub-group
// takes a snapshot of its parent's middlewares when it is created.
type Group struct {
	app    *App
	prefix string
	mws    []Middleware
	opts   []RouteOption
}

func cleanPrefix(p string) string {
	p = strings.TrimSuffix(p, "/")
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// Group creates a route group with a common prefix and optional middlewares.
func (a *App) Group(prefix string, mws ...Middleware) *Group {
	return &Group{
		app:    a,
		prefix: cleanPrefix(prefix),
		mws:    append([]Middleware(nil), mws...),
	}
}

// Group creates a sub-group that inherits this group's prefix and middlewares.
func (g *Group) Group(prefix string, mws ...Middleware) *Group {
	// Copy into a fresh slice so sibling groups never share a backing array.
	combined := make([]Middleware, 0, len(g.mws)+len(mws))
	combined = append(combined, g.mws...)
	combined = append(combined, mws...)
	return &Group{
		app:    g.app,
		prefix: g.prefix + cleanPrefix(prefix),
		mws:    combined,
		opts:   append([]RouteOption(nil), g.opts...),
	}
}

// Options sets route options (for example Tags or RequireAuth) that apply to
// every typed route registered in this group afterwards. Sub-groups created
// later inherit them, and options passed to a single route win.
func (g *Group) Options(opts ...RouteOption) {
	g.opts = append(g.opts, opts...)
}

// Use adds middlewares to the group.
func (g *Group) Use(m ...Middleware) {
	g.mws = append(g.mws, m...)
}

func (g *Group) wrap(h HandlerFunc) HandlerFunc {
	for i := len(g.mws) - 1; i >= 0; i-- {
		h = g.mws[i](h)
	}
	return h
}

// fullPath joins the group prefix and a route path. A path of "/" maps to the
// group prefix itself (an exact match, not a catch-all subtree).
func (g *Group) fullPath(path string) string {
	p := strings.TrimPrefix(path, "/")
	if p == "" {
		if g.prefix == "" {
			return "/{$}"
		}
		return g.prefix
	}
	return g.prefix + "/" + p
}

// Handle registers a plain route inside the group.
func (g *Group) Handle(method, path string, h HandlerFunc) {
	var once sync.Once
	var wrapped HandlerFunc
	g.app.Handle(method, g.fullPath(path), func(c *Ctx) error {
		once.Do(func() { wrapped = g.wrap(h) })
		return wrapped(c)
	})
}

// GroupRoute registers a type-safe route inside a Group.
func GroupRoute[In, Out any](g *Group, method, path string, fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	fullPath := g.fullPath(path)
	cfg := newRouteConfig(append(append([]RouteOption(nil), g.opts...), opts...))
	info := prepareInput(reflect.TypeFor[In](), method, fullPath)

	g.app.specs = append(g.app.specs, routeSpec{
		method: method, path: fullPath,
		in: reflect.TypeFor[In](), out: reflect.TypeFor[Out](),
		cfg: cfg,
	})
	g.Handle(method, path, typedHandler(cfg, info, fn))
	return Endpoint[In, Out]{Method: method, Path: fullPath}
}

// GroupGET registers a type-safe GET route inside a Group.
func GroupGET[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "GET", path, fn, o...)
}

// GroupPOST registers a type-safe POST route inside a Group.
func GroupPOST[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "POST", path, fn, o...)
}

// GroupPUT registers a type-safe PUT route inside a Group.
func GroupPUT[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "PUT", path, fn, o...)
}

// GroupPATCH registers a type-safe PATCH route inside a Group.
func GroupPATCH[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "PATCH", path, fn, o...)
}

// GroupDELETE registers a type-safe DELETE route inside a Group.
func GroupDELETE[In, Out any](g *Group, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, "DELETE", path, fn, o...)
}

// RegisterGroup registers a handler for an existing Endpoint inside a Group.
func RegisterGroup[In, Out any](g *Group, e Endpoint[In, Out], fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	return GroupRoute(g, e.Method, e.Path, fn, opts...)
}
