package aemeath_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestClientRoundTrip(t *testing.T) {
	srv := httptest.NewServer(newApp())
	defer srv.Close()
	ctx := context.Background()
	client := aemeath.NewClient(srv.URL)

	hello := aemeath.NewEndpoint[helloIn, helloOut]("GET", "/hello/{name}")
	out, err := hello.Call(ctx, client, helloIn{Name: "Aemeath", Lang: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message != "id:Aemeath" {
		t.Fatalf("message = %q", out.Message)
	}

	create := aemeath.NewEndpoint[userIn, userOut]("POST", "/users")
	u, err := create.Call(ctx, client, userIn{Name: "Aemeath"})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || u.Name != "Aemeath" {
		t.Fatalf("user = %+v", u)
	}

	_, err = create.Call(ctx, client, userIn{})
	var he *aemeath.HTTPError
	if !errors.As(err, &he) || he.Code != 400 {
		t.Fatalf("err = %v, want HTTPError 400", err)
	}
}

func TestClientPathEscaping(t *testing.T) {
	srv := httptest.NewServer(newApp())
	defer srv.Close()
	client := aemeath.NewClient(srv.URL)
	hello := aemeath.NewEndpoint[helloIn, helloOut]("GET", "/hello/{name}")
	out, err := hello.Call(context.Background(), client, helloIn{Name: "a b&c", Lang: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message != "id:a b&c" {
		t.Fatalf("message = %q", out.Message)
	}
}

func TestClientPathWithPointer(t *testing.T) {
	type ptrIn struct {
		ID *int `path:"id"`
	}
	type ptrOut struct {
		ID int `json:"id"`
	}
	app := aemeath.New()
	aemeath.GET(app, "/ptr/{id}", func(c *aemeath.Ctx, in ptrIn) (ptrOut, error) {
		if in.ID == nil {
			return ptrOut{}, aemeath.BadRequest("id kosong")
		}
		return ptrOut{ID: *in.ID}, nil
	})
	srv := httptest.NewServer(app)
	defer srv.Close()
	client := aemeath.NewClient(srv.URL)
	id := 42
	ep := aemeath.NewEndpoint[ptrIn, ptrOut]("GET", "/ptr/{id}")
	out, err := ep.Call(context.Background(), client, ptrIn{ID: &id})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != 42 {
		t.Fatalf("id = %d", out.ID)
	}
}

func TestClientErrorMessageFromServer(t *testing.T) {
	srv := httptest.NewServer(newApp())
	defer srv.Close()
	client := aemeath.NewClient(srv.URL)
	create := aemeath.NewEndpoint[userIn, userOut]("POST", "/users")
	_, err := create.Call(context.Background(), client, userIn{})
	var he *aemeath.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err bukan HTTPError: %v", err)
	}
	if !strings.Contains(he.Message, "wajib") {
		t.Fatalf("pesan error hilang, dapat %q", he.Message)
	}
}
