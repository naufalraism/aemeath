package aemeath_test

import (
	"strings"
	"testing"
)

func TestTypeScriptClient(t *testing.T) {
	ts := newApp().TypeScriptClient()
	for _, want := range []string{
		"export function createClient",
		"getHelloByName: (input: ",
		"path: { name: string }",
		"query?: { lang?: string }",
		"postUsers: (input: ",
		"body: { name: string }",
		"export interface UserOut {",
		"id: number;",
		"request<UserOut>",
		"getBoom: () =>",
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("not found: %q\n--- output ---\n%s", want, ts)
		}
	}
}
