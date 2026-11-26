package aemeath_test

import (
	"testing"

	"github.com/naufalraism/aemeath"
)

// The base URL is set once by NewClient and only read afterwards, like the
// client's other settings, so a Client stays safe for concurrent use.
func TestClientBaseURLIsReadOnly(t *testing.T) {
	c := aemeath.NewClient("https://api.example.com/")
	if got := c.BaseURL(); got != "https://api.example.com/" {
		t.Fatalf("BaseURL() = %q, want the URL given to NewClient", got)
	}
}
