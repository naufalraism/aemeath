package aemeath_test

import (
	"slices"
	"testing"

	"github.com/naufalraism/aemeath"
)

// Changing the list one caller got must not change what the next caller
// trusts: a shared slice would let any package make RealIP believe
// X-Forwarded-For from the whole internet.
func TestPrivateNetworksCannotBeChangedFromOutside(t *testing.T) {
	want := aemeath.PrivateNetworks()
	got := aemeath.PrivateNetworks()
	for i := range got {
		got[i] = "0.0.0.0/0"
	}
	_ = append(got[:0], "::/0")

	if again := aemeath.PrivateNetworks(); !slices.Equal(again, want) {
		t.Fatalf("PrivateNetworks() = %v after a caller changed its copy, want %v", again, want)
	}
	mw := aemeath.RealIP(aemeath.PrivateNetworks()...)
	if seen := remoteSeen(t, mw, "203.0.113.7:5000", map[string]string{"X-Forwarded-For": "1.1.1.1"}); seen != "203.0.113.7:5000" {
		t.Fatalf("a public client spoofed its address: RemoteAddr = %q", seen)
	}
}
