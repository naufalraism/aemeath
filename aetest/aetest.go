// Package aetest helps test Aemeath applications without a network.
//
//	client := aetest.NewClient(app)
//	out, err := api.CreateUser.Call(ctx, client, api.CreateUserIn{Name: "Ana"})
//
// The typed endpoint contract is used exactly as in production, but requests
// go straight into the application's ServeHTTP.
package aetest

import (
	"net/http"
	"net/http/httptest"

	"github.com/naufalraism/aemeath"
)

type transport struct{ h http.Handler }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if req.RemoteAddr == "" {
		req.RemoteAddr = "192.0.2.1:1234"
	}
	req.RequestURI = req.URL.RequestURI()
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	res := rec.Result()
	res.Request = req
	return res, nil
}

// NewClient returns a client whose requests are served by h in-process.
// Options (for example aemeath.WithHeader) are applied after the transport is set.
func NewClient(h http.Handler, opts ...aemeath.ClientOption) *aemeath.Client {
	all := append([]aemeath.ClientOption{
		aemeath.WithHTTPClient(&http.Client{Transport: transport{h}}),
	}, opts...)
	return aemeath.NewClient("http://aetest.invalid", all...)
}
