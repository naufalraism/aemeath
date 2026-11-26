package docclaims

import (
	"os/exec"
	"strings"
	"testing"
)

// pkg.go.dev opens every package with its doc comment. Without one, the first
// page a Go developer sees is an empty overview: the root package had none
// until v0.16.0, while every subpackage did.
func TestEveryPublicPackageHasADocComment(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}\t{{.Name}}\t{{.Doc}}", "./...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	checked := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		path, name, doc := parts[0], parts[1], parts[2]
		if strings.Contains(path+"/", "/internal/") {
			continue
		}
		checked++
		want := "Package " + name + " "
		if name == "main" {
			want = "Command "
		}
		if !strings.HasPrefix(doc, want) {
			t.Errorf("%s has no doc comment starting with %q (got %q)", path, want, doc)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d packages checked; the filter is wrong", checked)
	}
}
