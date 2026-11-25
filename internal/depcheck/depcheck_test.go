// Package depcheck guards what a project that imports one of the public
// packages has to download.
package depcheck

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
		}
		t.Fatalf("go list %v: %v\n%s", args, err, msg)
	}
	return strings.Fields(string(out))
}

// "go mod tidy" in a user's project also records the modules that the tests
// of every imported package need. A test of db that imported db/mysql made a
// PostgreSQL project download the MySQL driver; a test of cache that used
// miniredis made every cache user download it. Tests that need more than
// their package belongs under internal/, which no project imports.
func TestPublicPackageTestsNeedNoExtraModules(t *testing.T) {
	const mod = "{{with .Module}}{{.Path}}{{end}}"
	checked := 0
	for _, pkg := range goList(t, "./...") {
		rel := strings.TrimPrefix(pkg, "github.com/naufalraism/aemeath")
		if strings.Contains(rel+"/", "/internal/") || strings.HasPrefix(rel, "/examples/") || strings.HasPrefix(rel, "/benchmarks") {
			continue
		}
		checked++
		own := goList(t, "-deps", "-f", mod, pkg)
		for _, m := range goList(t, "-deps", "-test", "-f", mod, pkg) {
			if !slices.Contains(own, m) {
				t.Errorf("%s: its tests need %s, which the package itself does not import", pkg, m)
				own = append(own, m)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d public packages checked; the filter is wrong", checked)
	}
}
