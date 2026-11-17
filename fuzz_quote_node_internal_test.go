package aemeath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type flagIn struct {
	// Subdivision flags are built from "tag" characters U+E0020-U+E007F.
	Flag string `json:"flag" validate:"oneof=🏴󠁧󠁢󠁳󠁣󠁴󠁿 🏴󠁧󠁢󠁷󠁬󠁳󠁿"`
}

// The server and the generated Zod schema must accept the same values.
func TestZodEnumWithFlagEmoji(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	zodDir, err := filepath.Abs("examples/tscheck/node_modules/zod")
	if err != nil || !fileExistsForTest(zodDir) {
		t.Skip("zod is not installed (npm ci in examples/tscheck)")
	}
	app := New()
	registerRuntimeRoute(app, "POST", "/f", typeOf[flagIn](), typeOf[struct{}]())
	schemas := app.ZodSchemas()
	line := ""
	for _, l := range strings.Split(schemas, "\n") {
		if strings.Contains(l, "flag") {
			line = strings.TrimSpace(l)
		}
	}
	t.Logf("generated: %s", line)

	dir := t.TempDir()
	ts := strings.Replace(schemas, `from "zod"`, `from "`+filepath.ToSlash(zodDir)+`/index.js"`, 1)
	js := strings.ReplaceAll(ts, "export const", "const") // plain JavaScript: the schemas file has no type syntax except "export"
	js = strings.Replace(js, `import { z } from`, `const { z } = await import(`, 1)
	js = strings.Replace(js, `/index.js";`, `/index.js");`, 1)
	js += "\nconst s = postFBodySchema;\n" +
		"for (const v of ['\\u{1F3F4}\\u{E0067}\\u{E0062}\\u{E0073}\\u{E0063}\\u{E0074}\\u{E007F}', 'nope']) console.log(JSON.stringify(v), s.safeParse({flag: v}).success);\n"
	file := filepath.Join(dir, "check.mjs")
	if err := os.WriteFile(file, []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file).CombinedOutput()
	t.Logf("node:\n%s", out)
	if err != nil {
		t.Fatalf("node: %v", err)
	}

	rec := doJSON(app, "POST", "/f", `{"flag":"🏴󠁧󠁢󠁳󠁣󠁴󠁿"}`)
	t.Logf("server, the same value: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	if rec.Code != 200 {
		t.Fatalf("the server refuses the value it lists")
	}
	if !strings.Contains(string(out), `"🏴󠁧󠁢󠁳󠁣󠁴󠁿" true`) {
		t.Error("the server accepts the Scottish flag, the generated Zod schema rejects it")
	}
}
