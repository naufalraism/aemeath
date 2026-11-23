package aemeath_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/naufalraism/aemeath"
)

type cookieIn struct {
	Sess string `cookie:"sess" json:"-"`
}

type cookieOut struct {
	Sess string `json:"sess"`
}

// A cookie value cannot hold ";", a double quote, a backslash or anything
// outside printable ASCII. net/http dropped those bytes and only logged it,
// so the server received "ab" for "a;b" and "" for "é", and the call
// succeeded. Such a value is now an error, and nothing is sent.
func TestClientRefusesCookieValuesItCannotSend(t *testing.T) {
	var hits atomic.Int32
	app := aemeath.New()
	aemeath.GET(app, "/c", func(c *aemeath.Ctx, in cookieIn) (cookieOut, error) {
		hits.Add(1)
		return cookieOut(in), nil
	})
	srv := httptest.NewServer(app)
	defer srv.Close()
	ep := aemeath.NewEndpoint[cookieIn, cookieOut]("GET", "/c")
	c := aemeath.NewClient(srv.URL)

	for _, v := range []string{"abc123", "a b", "a,b", "x=y"} {
		out, err := ep.Call(context.Background(), c, cookieIn{v})
		if err != nil || out.Sess != v {
			t.Errorf("cookie %q -> %q, %v; want it unchanged", v, out.Sess, err)
		}
	}
	before := hits.Load()
	for _, v := range []string{"a;b", `a"b`, `a\b`, "é", "tab\there"} {
		out, err := ep.Call(context.Background(), c, cookieIn{v})
		if err == nil || !strings.Contains(err.Error(), `cookie "sess"`) {
			t.Errorf("cookie %q -> %q, err %v; want an error naming the cookie", v, out.Sess, err)
		}
	}
	if got := hits.Load() - before; got != 0 {
		t.Errorf("%d requests reached the server with a value that had to be altered", got)
	}
}
