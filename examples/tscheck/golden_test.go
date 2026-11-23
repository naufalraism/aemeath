package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden with the current output")

// generated returns what the generators produce for this app, by file name.
func generated(t *testing.T) map[string][]byte {
	t.Helper()
	app := newApp()
	spec, err := json.MarshalIndent(app.OpenAPI(docsInfo), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{
		"client.ts":    []byte(app.TypeScriptClient()),
		"schemas.ts":   []byte(app.ZodSchemas()),
		"hooks.ts":     []byte(app.ReactQueryHooks()),
		"openapi.json": append(spec, '\n'),
	}
}

// The generated client, Zod schemas, React Query hooks and OpenAPI document
// are what users' frontends are built on. Any change to them must be seen in
// review: this test compares them with testdata/golden. After an intended
// change, run
//
//	go test ./examples/tscheck -update
//
// and commit the new files with the change that caused them.
func TestGeneratedFilesMatchTheGoldenFiles(t *testing.T) {
	dir := filepath.Join("testdata", "golden")
	got := generated(t)
	if *update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, b := range got {
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for name, b := range got {
		want, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%v (run: go test ./examples/tscheck -update)", err)
		}
		if !bytes.Equal(b, want) {
			t.Errorf("%s differs from testdata/golden/%s:\n%s\nIf the change is intended, run: go test ./examples/tscheck -update", name, name, firstDifference(string(want), string(b)))
		}
	}
}

// The output must not depend on map order or timing: two runs give the same bytes.
func TestGeneratedFilesAreDeterministic(t *testing.T) {
	a, b := generated(t), generated(t)
	for name := range a {
		if !bytes.Equal(a[name], b[name]) {
			t.Errorf("%s differs between two runs:\n%s", name, firstDifference(string(a[name]), string(b[name])))
		}
	}
}

// firstDifference shows the first line that differs, with its number.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return "line " + strconv.Itoa(i+1) + ":\n  golden: " + wl + "\n  now:    " + gl
		}
	}
	return "(same lines, different bytes)"
}
