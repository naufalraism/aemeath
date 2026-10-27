package aemeath_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

type zAddress struct {
	City string `json:"city" validate:"required,min=2"`
}

type zUser struct {
	ID      int      `json:"id"`
	Friends []*zUser `json:"friends,omitempty"`
	Nick    *string  `json:"nick"`
}

type zSignup struct {
	Name    string   `json:"name" validate:"required,min=3,max=10"`
	Email   string   `json:"email" validate:"required,email"`
	Role    string   `json:"role" validate:"oneof=admin user"`
	Age     *int     `json:"age" validate:"required,gte=18,lt=120"`
	Level   int      `json:"level" validate:"oneof=1 2 3"`
	Tags    []string `json:"tags" validate:"max=2"`
	Code    string   `json:"code" validate:"pattern=^[A-Z]{3}-[0-9]+$"`
	Address zAddress `json:"address"`
	Nick    *string  `json:"nick"`
	Site    string   `json:"site" validate:"url"`
}

func zodApp() *aemeath.App {
	app := aemeath.New()
	aemeath.POST(app, "/signup", func(*aemeath.Ctx, zSignup) (zUser, error) { return zUser{}, nil })
	aemeath.GET(app, "/users", func(*aemeath.Ctx, struct {
		Tag string `query:"tag"`
	}) ([]zUser, error) {
		return nil, nil
	})
	aemeath.GET(app, "/users/{id}", func(*aemeath.Ctx, struct {
		ID int `path:"id"`
	}) (zUser, error) {
		return zUser{}, nil
	})
	aemeath.DELETE(app, "/users/{id}", func(*aemeath.Ctx, struct {
		ID int `path:"id"`
	}) (struct{}, error) {
		return struct{}{}, nil
	}, aemeath.Status(204))
	aemeath.PUT(app, "/users/{id}", func(*aemeath.Ctx, struct {
		ID    int    `path:"id"`
		Dry   bool   `query:"dry"`
		Trace string `header:"X-Trace"`
		Name  string `json:"name" validate:"required"`
		Bio   string `json:"bio"`
	}) (zUser, error) {
		return zUser{}, nil
	})
	aemeath.GET(app, "/ping", func(*aemeath.Ctx, struct{}) (map[string]string, error) { return nil, nil })
	return app
}

func TestZodSchemas(t *testing.T) {
	out := zodApp().ZodSchemas()
	for _, want := range []string{
		`import { z } from "zod";`,
		`export const ZUserSchema: z.ZodType<ZUser> = z.object({`,
		`friends: z.array(z.lazy(() => ZUserSchema)).nullable().optional()`, // recursive type, optional + nullable
		`nick: z.string().nullable()`,
		`export const postSignupBodySchema = z.object({`,
		`name: z.string().min(3).max(10)`,
		`email: z.string().email()`,
		`role: z.enum(["admin", "user"]).optional()`,
		`age: z.number().int().gte(18).lt(120),`, // required pointer: not nullable, not optional
		`level: z.union([z.literal(1), z.literal(2), z.literal(3)]).optional()`,
		`tags: z.array(z.string()).max(2).optional()`,
		`code: z.string().regex(new RegExp("^[A-Z]{3}-[0-9]+$")).optional()`,
		`address: z.lazy(() => ZAddressSchema),`, // non-pointer struct with required fields: required
		`site: z.string().url().optional()`,
		`export const getUsersQuerySchema = z.object({`,
		`export const getUsersByIdPathSchema = z.object({`,
		`id: z.number().int(),`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("schemas.ts is missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "session") {
		t.Error("cookie parameters must not be generated")
	}
}

func TestReactQueryHooks(t *testing.T) {
	out := zodApp().ReactQueryHooks()
	for _, want := range []string{
		`import { useMutation, useQuery`,
		`useGetUsers: (input?: Input<"getUsers">`,                                  // GET with optional input: query
		`useGetUsersById: (input: Input<"getUsersById">`,                           // GET with required input
		`queryKey: ["getUsersById", input]`,                                        //
		`usePostSignup: (options?: Omit<UseMutationOptions<Output<"postSignup">`,   // POST: mutation
		`useDeleteUsersById: (options?: Omit<UseMutationOptions<`,                  // DELETE: mutation
		`useGetPing: (options?: Omit<UseQueryOptions<Output<"getPing">, ApiError>`, // no input: no argument
		`queryFn: () => client.getPing()`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hooks.ts is missing %q\n%s", want, out)
		}
	}
}

func TestEnableClientServesAllThreeFiles(t *testing.T) {
	app := zodApp()
	app.EnableClient()
	for path, want := range map[string]string{
		"/client.ts":  "export function createClient",
		"/schemas.ts": "import { z } from \"zod\"",
		"/hooks.ts":   "export function createHooks",
	} {
		rec := get(app, path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: status = %d, body does not contain %q", path, rec.Code, want)
		}
	}
	// A route registered after the first request must show up.
	aemeath.GET(app, "/late", func(*aemeath.Ctx, struct{}) (map[string]string, error) { return nil, nil })
	if !strings.Contains(get(app, "/client.ts").Body.String(), "getLate") {
		t.Error("the cached client was not regenerated after a new route was registered")
	}
}

func TestWriteTypeScript(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "web", "api")
	if err := zodApp().WriteTypeScript(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client.ts", "schemas.ts", "hooks.ts"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.HasPrefix(string(b), "// Generated by Aemeath") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOperationInputPartsComeInAFixedOrder(t *testing.T) {
	out := zodApp().ZodSchemas()
	last := -1
	for _, name := range []string{"putUsersByIdPathSchema", "putUsersByIdQuerySchema", "putUsersByIdHeadersSchema", "putUsersByIdBodySchema"} {
		i := strings.Index(out, "export const "+name)
		if i < 0 || i < last {
			t.Fatalf("%s is missing or out of order (Path, Query, Headers, Body)\n%s", name, out)
		}
		last = i
	}
}

func TestGeneratedOutputIsDeterministic(t *testing.T) {
	first := zodApp().ZodSchemas() + zodApp().TypeScriptClient() + zodApp().ReactQueryHooks()
	for i := 0; i < 30; i++ {
		if again := zodApp().ZodSchemas() + zodApp().TypeScriptClient() + zodApp().ReactQueryHooks(); again != first {
			t.Fatal("the generated TypeScript differs between runs; map iteration order leaked into the output")
		}
	}
}

func TestWriteTypeScriptSkipsUnchangedFilesAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	app := zodApp()
	if err := app.WriteTypeScript(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "client.ts")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	if err := app.WriteTypeScript(dir); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(old) {
		t.Fatal("an unchanged file was rewritten, which would make a bundler reload for nothing")
	}

	// A new route changes the content, so the file is rewritten.
	aemeath.GET(app, "/late", func(*aemeath.Ctx, struct{}) (map[string]string, error) { return nil, nil })
	if err := app.WriteTypeScript(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "getLate") {
		t.Fatal("a changed client.ts was not rewritten")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestListenWritesTypeScriptAndExitsWhenAskedByTheCLI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	t.Setenv(aemeath.EnvTypeScriptOut, dir)

	done := make(chan error, 1)
	go func() { done <- zodApp().Listen("127.0.0.1:0") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Listen returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Listen started serving instead of writing the TypeScript and returning")
	}
	for _, name := range []string{"client.ts", "schemas.ts", "hooks.ts"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
}
