package aemeath_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

type rtIn struct {
	ID       int       `path:"id" json:"-"`
	Q        string    `query:"q" json:"-"`
	Tags     []string  `query:"tag" json:"-"`
	Zero     *int      `query:"zero" json:"-"`
	Missing  *int      `query:"missing" json:"-"`
	When     time.Time `query:"when" json:"-"`
	Token    string    `header:"X-Token" json:"-"`
	Sess     string    `cookie:"sess" json:"-"`
	Name     string    `json:"name"`
	Nickname *string   `json:"nickname"`
}

type rtOut struct {
	ID       int       `json:"id"`
	Q        string    `json:"q"`
	Tags     []string  `json:"tags"`
	Zero     *int      `json:"zero"`
	Missing  *int      `json:"missing"`
	When     time.Time `json:"when"`
	Token    string    `json:"token"`
	Sess     string    `json:"sess"`
	Name     string    `json:"name"`
	Nickname *string   `json:"nickname"`
}

// One endpoint value shared by server and client: every parameter source
// and the body arrive as sent, including values that need escaping, a
// pointer to zero (sent) next to a nil pointer (not sent), repeated query
// keys and a time with nanoseconds and a zone.
func TestClientRoundTripEverySource(t *testing.T) {
	ep := aemeath.NewEndpoint[rtIn, rtOut]("POST", "/items/{id}")
	app := aemeath.New()
	aemeath.Register(app, ep, func(c *aemeath.Ctx, in rtIn) (rtOut, error) {
		return rtOut{in.ID, in.Q, in.Tags, in.Zero, in.Missing, in.When, in.Token, in.Sess, in.Name, in.Nickname}, nil
	})
	srv := httptest.NewServer(app)
	defer srv.Close()

	zero, nick := 0, "ani"
	in := rtIn{
		ID: 7, Q: "a b&c=d/é", Tags: []string{"x", "y z"}, Zero: &zero,
		When:  time.Date(2026, 11, 24, 5, 0, 0, 123456789, time.FixedZone("WIB", 7*3600)),
		Token: "Bearer abc", Sess: "s 1,2", Name: "Ana", Nickname: &nick,
	}
	out, err := ep.Call(context.Background(), aemeath.NewClient(srv.URL+"/"), in)
	if err != nil {
		t.Fatal(err)
	}
	want := rtOut{7, in.Q, in.Tags, &zero, nil, in.When, in.Token, in.Sess, in.Name, &nick}
	if !out.When.Equal(want.When) {
		t.Errorf("When = %v, want %v", out.When, want.When)
	}
	out.When, want.When = time.Time{}, time.Time{}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", out, want)
	}
}

func TestClientErrorsAndEdgeResponses(t *testing.T) {
	type in struct {
		Email string `json:"email" validate:"required,email"`
	}
	type out struct {
		OK bool `json:"ok"`
	}
	app := aemeath.New()
	aemeath.POST(app, "/validate", func(c *aemeath.Ctx, _ in) (out, error) { return out{true}, nil })
	app.Handle("GET", "/proxy-error", func(c *aemeath.Ctx) error { return c.Text(http.StatusBadGateway, "<html>bad gateway</html>") })
	app.Handle("GET", "/empty", func(c *aemeath.Ctx) error { c.W.WriteHeader(http.StatusNoContent); return nil })
	app.Handle("GET", "/not-json", func(c *aemeath.Ctx) error { return c.Text(http.StatusOK, "hello") })
	app.Handle("GET", "/huge", func(c *aemeath.Ctx) error {
		c.W.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(c.W, `"`+strings.Repeat("a", 32<<20)+`"`)
		return nil
	})
	srv := httptest.NewServer(app)
	defer srv.Close()
	c := aemeath.NewClient(srv.URL)
	ctx := context.Background()

	t.Run("422 carries the field errors", func(t *testing.T) {
		_, err := aemeath.NewEndpoint[in, out]("POST", "/validate").Call(ctx, c, in{Email: "nope"})
		var he *aemeath.HTTPError
		if !errors.As(err, &he) || he.Code != 422 || len(he.Errors) != 1 || he.Errors[0].Field != "email" || he.Errors[0].Rule != "email" {
			t.Fatalf("err = %#v", err)
		}
	})
	t.Run("an error page that is not JSON keeps its status", func(t *testing.T) {
		_, err := aemeath.NewEndpoint[struct{}, out]("GET", "/proxy-error").Call(ctx, c, struct{}{})
		var he *aemeath.HTTPError
		if !errors.As(err, &he) || he.Code != 502 || he.Message != "Bad Gateway" {
			t.Fatalf("err = %#v", err)
		}
	})
	t.Run("204 with no body is the zero value", func(t *testing.T) {
		got, err := aemeath.NewEndpoint[struct{}, out]("GET", "/empty").Call(ctx, c, struct{}{})
		if err != nil || got.OK {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("a 200 that is not JSON is an error", func(t *testing.T) {
		_, err := aemeath.NewEndpoint[struct{}, out]("GET", "/not-json").Call(ctx, c, struct{}{})
		if err == nil || !strings.Contains(err.Error(), "failed to decode response") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a response over 32 MiB is refused", func(t *testing.T) {
		_, err := aemeath.NewEndpoint[struct{}, string]("GET", "/huge").Call(ctx, c, struct{}{})
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an empty path parameter is refused before sending", func(t *testing.T) {
		type p struct {
			ID string `path:"id"`
		}
		_, err := aemeath.NewEndpoint[p, out]("GET", "/items/{id}").Call(ctx, c, p{})
		if err == nil || !strings.Contains(err.Error(), `path param "id" is empty`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a cancelled context stops the call", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := aemeath.NewEndpoint[struct{}, out]("GET", "/empty").Call(cctx, c, struct{}{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an unreachable server is an error, not a hang", func(t *testing.T) {
		dead := httptest.NewServer(http.NotFoundHandler())
		dead.Close()
		_, err := aemeath.NewEndpoint[struct{}, out]("GET", "/").Call(ctx, aemeath.NewClient(dead.URL), struct{}{})
		if err == nil {
			t.Fatal("no error")
		}
	})
}

// A field that is a parameter travels as that parameter only, even without
// json:"-": the body the client sends leaves it out, so the server never sees
// a second, possibly different, copy.
func TestClientKeepsParametersOutOfTheBody(t *testing.T) {
	type in struct {
		ID    int    `path:"id" json:"id"`
		Page  int    `query:"page" json:"page"`
		Token string `header:"X-Token" json:"token"`
		Name  string `json:"name"`
	}
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	_, err := aemeath.NewEndpoint[in, struct{}]("PUT", "/items/{id}").Call(context.Background(), aemeath.NewClient(srv.URL), in{ID: 7, Page: 2, Token: "t", Name: "Ana"})
	if err != nil {
		t.Fatal(err)
	}
	if body != `{"name":"Ana"}` {
		t.Errorf("body = %s, want only the non-parameter field", body)
	}
}
