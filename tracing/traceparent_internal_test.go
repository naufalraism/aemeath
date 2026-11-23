package tracing

import (
	"strings"
	"testing"
)

const parentTrace = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestParseTraceparentRejectsGarbage(t *testing.T) {
	good := "00-" + parentTrace + "-00f067aa0ba902b7-01"
	if _, _, s, ok := parseTraceparent(good); !ok || !s {
		t.Fatal("valid header rejected")
	}
	for _, bad := range []string{
		"", "garbage", "00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01",
		"00-" + parentTrace + "-" + strings.Repeat("0", 16) + "-01",
		"00-" + strings.ToUpper(parentTrace) + "-00f067aa0ba902b7-01",
		"ff-" + parentTrace + "-00f067aa0ba902b7-01",
		"00-" + parentTrace + "-00f067aa0ba902b7-01-extra",
		"00-" + parentTrace + "-00f067aa0ba902b7-zz",
	} {
		if _, _, _, ok := parseTraceparent(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
