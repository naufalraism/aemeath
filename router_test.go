package aemeath_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type routerIn struct {
	ID   int    `path:"id" json:"-"`
	Name string `json:"name"`
}

type routerOut struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	By   string `json:"by"`
}

// GET, POST, Route and Register take a Group as well as an App, so the
// GroupGET family is no longer needed. The same routes registered both ways
// must serve the same responses, run the group's middleware and options, and
// produce the same OpenAPI document and TypeScript client.
func TestRouteFunctionsAcceptAGroup(t *testing.T) {
	build := func(viaGroupFuncs bool) *aemeath.App {
		app := aemeath.New()
		g := app.Group("/api", func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
			return func(c *aemeath.Ctx) error {
				c.W.Header().Set("X-Group", "yes")
				return next(c)
			}
		})
		g.Options(aemeath.Tags("items"))
		v1 := g.Group("/v1")
		h := func(c *aemeath.Ctx, in routerIn) (routerOut, error) {
			return routerOut{in.ID, in.Name, c.R.Method}, nil
		}
		ep := aemeath.Endpoint[routerIn, routerOut]{Method: "PATCH", Path: "/items/{id}"}
		if viaGroupFuncs {
			aemeath.GroupGET(v1, "/items/{id}", h)
			aemeath.GroupPOST(v1, "/items/{id}", h, aemeath.Status(201))
			aemeath.GroupPUT(v1, "/items/{id}", h)
			aemeath.GroupDELETE(v1, "/items/{id}", h)
			aemeath.GroupRoute(v1, "OPTIONS", "/items/{id}", h)
			aemeath.RegisterGroup(v1, ep, h)
		} else {
			aemeath.GET(v1, "/items/{id}", h)
			aemeath.POST(v1, "/items/{id}", h, aemeath.Status(201))
			aemeath.PUT(v1, "/items/{id}", h)
			aemeath.DELETE(v1, "/items/{id}", h)
			aemeath.Route(v1, "OPTIONS", "/items/{id}", h)
			aemeath.Register(v1, ep, h)
		}
		return app
	}
	oldApp, newApp := build(true), build(false)

	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"} {
		call := func(app *aemeath.App) (int, string, string) {
			req := httptest.NewRequest(m, "/api/v1/items/7", strings.NewReader(`{"name":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			b, _ := io.ReadAll(rec.Body)
			return rec.Code, rec.Header().Get("X-Group"), string(b)
		}
		oc, oh, ob := call(oldApp)
		nc, nh, nb := call(newApp)
		if nc != oc || nh != oh || nb != ob {
			t.Errorf("%s: GroupX gave %d %q %s, X(group) gave %d %q %s", m, oc, oh, ob, nc, nh, nb)
		}
		if m != "PATCH" && nh != "yes" {
			t.Errorf("%s: group middleware did not run (status %d)", m, nc)
		}
	}

	// PATCH was registered from an Endpoint whose path the group prefixes,
	// exactly as RegisterGroup always did.
	if rec := httptest.NewRecorder(); true {
		newApp.ServeHTTP(rec, httptest.NewRequest("PATCH", "/api/v1/items/7", strings.NewReader(`{}`)))
		if rec.Code != http.StatusOK {
			t.Errorf("PATCH via Register(group, endpoint): status %d", rec.Code)
		}
	}

	o, _ := json.Marshal(oldApp.OpenAPI(aemeath.Info{Title: "t", Version: "1"}))
	n, _ := json.Marshal(newApp.OpenAPI(aemeath.Info{Title: "t", Version: "1"}))
	if string(o) != string(n) {
		t.Errorf("OpenAPI differs:\nGroupX:   %s\nX(group): %s", o, n)
	}
	// The comparison above cannot catch a mistake shared by both ways, since
	// the GroupX functions now call the new ones, so check the result itself.
	var doc struct {
		Paths map[string]map[string]struct {
			Tags      []string                   `json:"tags"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(n, &doc); err != nil {
		t.Fatal(err)
	}
	ops := doc.Paths["/api/v1/items/{id}"]
	for _, m := range []string{"get", "post", "put", "delete", "patch"} {
		op, ok := ops[m]
		if !ok {
			t.Errorf("OpenAPI has no %s /api/v1/items/{id}: %s", m, n)
			continue
		}
		if strings.Join(op.Tags, ",") != "items" {
			t.Errorf("%s: tags = %v, want the group's [items]", m, op.Tags)
		}
	}
	if _, ok := ops["post"].Responses["201"]; !ok {
		t.Errorf("post: the route's Status(201) option is missing: %v", ops["post"].Responses)
	}
	if oldApp.TypeScriptClient() != newApp.TypeScriptClient() {
		t.Error("TypeScript clients differ")
	}
}

// An App works through the same functions, as before.
func TestRouteFunctionsStillAcceptAnApp(t *testing.T) {
	app := aemeath.New()
	ep := aemeath.GET(app, "/ping", func(*aemeath.Ctx, struct{}) (string, error) { return "pong", nil })
	if ep.Path != "/ping" || ep.Method != "GET" {
		t.Fatalf("endpoint = %+v", ep)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/ping", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
