package aemeath_test

import (
	"bytes"
	"flag"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateAPI = flag.Bool("update-api", false, "rewrite testdata/api.txt from the current code")

// Every exported name is a promise to the people who use the framework:
// after v1.0 it cannot change without a v2. testdata/api.txt lists all of
// them (functions and methods with their signatures, types, struct fields,
// interface methods, constants and variables) so that a change to the
// public API is never an accident: it shows up in the diff of that file.
//
// After an intended change, run: go test -run TestPublicAPI -update-api .
func TestPublicAPI(t *testing.T) {
	got := publicAPI(t)
	const golden = "testdata/api.txt"
	if *updateAPI {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run: go test -run TestPublicAPI -update-api .)", err)
	}
	if got == strings.ReplaceAll(string(want), "\r\n", "\n") {
		return
	}
	gotLines, wantLines := lineSet(got), lineSet(string(want))
	for _, l := range strings.Split(strings.TrimSpace(string(want)), "\n") {
		if !gotLines[strings.TrimRight(l, "\r")] {
			t.Errorf("removed or changed: %s", l)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		if !wantLines[l] {
			t.Errorf("added: %s", l)
		}
	}
	t.Log("if the change is intended, run: go test -run TestPublicAPI -update-api .")
}

func lineSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(s, "\n") {
		m[strings.TrimRight(l, "\r")] = true
	}
	return m
}

// publicAPI lists the exported API of every package outside internal/ and
// examples/, one declaration per line, sorted.
func publicAPI(t *testing.T) string {
	t.Helper()
	var lines []string
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH = "linux", "amd64" // the same list on every machine
	err := filepath.WalkDir(".", func(dir string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		name := d.Name()
		if dir != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
			name == "internal" || name == "examples" || name == "testdata" || name == "node_modules" ||
			name == "docs" || name == "benchmarks") {
			return filepath.SkipDir
		}
		pkg, err := ctx.ImportDir(dir, 0)
		if err != nil {
			return nil // no Go files here
		}
		prefix := "aemeath"
		if dir != "." {
			prefix = filepath.ToSlash(dir)
		}
		fset := token.NewFileSet()
		for _, file := range pkg.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(dir, file), nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			lines = append(lines, declarations(fset, prefix, f)...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func declarations(fset *token.FileSet, pkg string, f *ast.File) []string {
	src := func(n ast.Node) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, n)
		return strings.Join(strings.Fields(b.String()), " ")
	}
	var out []string
	add := func(format string, args ...string) {
		out = append(out, pkg+": "+strings.Join(append([]string{format}, args...), " "))
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			sig := strings.TrimPrefix(src(d.Type), "func")
			if d.Recv == nil {
				add("func", d.Name.Name+sig)
				continue
			}
			recv := receiverName(d.Recv.List[0].Type)
			if ast.IsExported(recv) {
				add("method", "("+src(d.Recv.List[0].Type)+")", d.Name.Name+sig)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					switch tt := s.Type.(type) {
					case *ast.StructType:
						add("type", s.Name.Name, "struct")
						for _, fl := range tt.Fields.List {
							names := fl.Names
							if len(names) == 0 { // embedded
								names = []*ast.Ident{{Name: receiverName(fl.Type)}}
							}
							for _, n := range names {
								if ast.IsExported(n.Name) {
									add("field", s.Name.Name+"."+n.Name, src(fl.Type))
								}
							}
						}
					case *ast.InterfaceType:
						add("type", s.Name.Name, "interface")
						for _, m := range tt.Methods.List {
							for _, n := range m.Names {
								add("imethod", s.Name.Name+"."+n.Name+strings.TrimPrefix(src(m.Type), "func"))
							}
							if len(m.Names) == 0 {
								add("imethod", s.Name.Name, "embeds", src(m.Type))
							}
						}
					default:
						eq := ""
						if s.Assign.IsValid() {
							eq = "= "
						}
						add("type", s.Name.Name, eq+src(s.Type))
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if !n.IsExported() {
							continue
						}
						typ := ""
						if s.Type != nil {
							typ = " " + src(s.Type)
						}
						add(d.Tok.String(), n.Name+typ)
					}
				}
			}
		}
	}
	return out
}

// receiverName is the type name in a receiver or embedded field: T, *T, T[K], pkg.T.
func receiverName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return receiverName(x.X)
	case *ast.IndexExpr:
		return receiverName(x.X)
	case *ast.IndexListExpr:
		return receiverName(x.X)
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.Ident:
		return x.Name
	}
	return ""
}
