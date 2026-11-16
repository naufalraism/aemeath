package aemeath

import (
	"strings"
	"testing"
	"time"
)

// An int64/uint64 above 2^53 silently changes in JavaScript. The generated
// types now say so on such fields, and point to json:",string".
func TestWide64FieldsCarryAWarning(t *testing.T) {
	type out struct {
		ID      uint64        `json:"id"`
		Ref     *int64        `json:"ref"`
		Safe    uint64        `json:"safe,string"`
		Count   int           `json:"count"`
		Small   int32         `json:"small"`
		Elapsed time.Duration `json:"elapsed"`
	}
	a := New()
	GET(a, "/w", func(c *Ctx, _ struct{}) (out, error) { return out{}, nil })
	ts := a.TypeScriptClient()
	line := func(field string) string {
		for _, l := range strings.Split(ts, "\n") {
			if strings.Contains(l, " "+field+":") || strings.Contains(l, " "+field+"?:") {
				return l
			}
		}
		t.Fatalf("field %s not found", field)
		return ""
	}
	for field, want := range map[string]bool{"id": true, "ref": true, "safe": false, "count": false, "small": false, "elapsed": false} {
		if got := strings.Contains(line(field), "loses precision"); got != want {
			t.Errorf("%s: warning=%v, want %v: %s", field, got, want, line(field))
		}
	}
	if !strings.Contains(line("id"), `json:",string"`) {
		t.Errorf("the warning should name the fix: %s", line("id"))
	}
}
