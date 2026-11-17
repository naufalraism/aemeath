package aemeath_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/naufalraism/aemeath"
)

// A handler that fails because its client left (context.Canceled, maybe
// wrapped by a database driver) is not a server error.
func TestCanceledIsClientClosedRequest(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{context.Canceled, aemeath.StatusClientClosedRequest},
		{fmt.Errorf("query users: %w", context.Canceled), aemeath.StatusClientClosedRequest},
		{context.DeadlineExceeded, http.StatusInternalServerError}, // a server-side timeout stays a 500
		{errors.New("boom"), http.StatusInternalServerError},
		{aemeath.NotFound("x"), http.StatusNotFound},
	}
	for _, tc := range cases {
		if got := aemeath.AsHTTPError(tc.err).Code; got != tc.want {
			t.Errorf("AsHTTPError(%v).Code = %d, want %d", tc.err, got, tc.want)
		}
	}
}
