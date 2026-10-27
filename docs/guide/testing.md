# Testing

`aetest` gives you a typed client that calls your application in-process, with no network and no port.

```go
func TestCreateUser(t *testing.T) {
	app := newApp()                         // your constructor
	client := aetest.NewClient(app)

	user, err := api.CreateUser.Call(context.Background(), client, api.CreateUserIn{Name: "Ana", Email: "ana@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = api.CreateUser.Call(context.Background(), client, api.CreateUserIn{})
	var he *aemeath.HTTPError
	if !errors.As(err, &he) || he.Code != 422 {
		t.Fatalf("want a 422, got %v", err)
	}
}
```

The same endpoint contract is used in production and in tests, so a renamed field or a changed route breaks the test at compile time.

For raw requests use `httptest` with `app.ServeHTTP`. To fuzz the request binder of your own routes, see `FuzzBind` in the framework repository.
