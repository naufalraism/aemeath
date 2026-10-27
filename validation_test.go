package aemeath_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

type signupIn struct {
	Name     string   `json:"name" validate:"required,min=3,max=10"`
	Email    string   `json:"email" validate:"required,email"`
	Role     string   `json:"role" validate:"oneof=admin user guest"`
	Age      *int     `json:"age" validate:"required,gte=18,lt=120"`
	Website  string   `json:"website" validate:"url"`
	Token    string   `json:"token" validate:"uuid"`
	Code     string   `json:"code" validate:"pattern=^[A-Z]{3}-[0-9]+$"`
	Tags     []string `json:"tags" validate:"max=2"`
	Priority int      `json:"priority" validate:"oneof=1 2 3"`
	Zip      string   `json:"zip" validate:"len=5"`
}

func signupApp() *aemeath.App {
	app := aemeath.New()
	aemeath.POST(app, "/signup", func(_ *aemeath.Ctx, in signupIn) (map[string]string, error) {
		return map[string]string{"ok": in.Name}, nil
	})
	return app
}

type errBody struct {
	Error  string `json:"error"`
	Errors []struct {
		Field   string `json:"field"`
		Rule    string `json:"rule"`
		Message string `json:"message"`
	} `json:"errors"`
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errBody {
	t.Helper()
	var e errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("response is not an error body: %s", rec.Body)
	}
	return e
}

func (e errBody) has(field, rule string) bool {
	for _, x := range e.Errors {
		if x.Field == field && x.Rule == rule {
			return true
		}
	}
	return false
}

func TestValidationRulesAccept(t *testing.T) {
	rec := do(signupApp(), "POST", "/signup", `{
		"name":"Aemeath","email":"a@example.com","role":"admin","age":30,
		"website":"https://example.com/x","token":"123e4567-e89b-12d3-a456-426614174000",
		"code":"ABC-123","tags":["a","b"],"priority":2,"zip":"12345"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestValidationCollectsAllErrors(t *testing.T) {
	rec := do(signupApp(), "POST", "/signup", `{
		"name":"ab","email":"not-an-email","role":"root","age":17,
		"website":"ftp://x","token":"nope","code":"abc","tags":["a","b","c"],"priority":9,"zip":"123"}`)
	if rec.Code != 422 {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	e := decodeErr(t, rec)
	if e.Error != "validation failed" {
		t.Fatalf("error = %q", e.Error)
	}
	for _, want := range [][2]string{
		{"name", "min"}, {"email", "email"}, {"role", "oneof"}, {"age", "gte"}, {"website", "url"},
		{"token", "uuid"}, {"code", "pattern"}, {"tags", "max"}, {"priority", "oneof"}, {"zip", "len"},
	} {
		if !e.has(want[0], want[1]) {
			t.Errorf("missing error %v in %s", want, rec.Body)
		}
	}
	if len(e.Errors) != 10 {
		t.Errorf("got %d errors, want 10: %s", len(e.Errors), rec.Body)
	}
}

func TestValidationBoundaries(t *testing.T) {
	cases := []struct {
		body        string
		field, rule string // rule "" means the body must be accepted
	}{
		{`{"name":"abc","email":"a@b.co","age":18}`, "", ""},        // min is inclusive
		{`{"name":"abcdefghij","email":"a@b.co","age":18}`, "", ""}, // max is inclusive
		{`{"name":"abcdefghijk","email":"a@b.co","age":18}`, "name", "max"},
		{`{"name":"abc","email":"a@b.co","age":119}`, "", ""},
		{`{"name":"abc","email":"a@b.co","age":120}`, "age", "lt"},
		{`{"name":"日本語","email":"a@b.co","age":18}`, "", ""}, // length counts characters, not bytes
		{`{"name":"abc","email":"a@b.co"}`, "age", "required"},
		{`{"name":"abc","email":"a@b.co","age":0}`, "age", "gte"}, // a pointer to zero IS a provided value
		{`{"name":"abc","email":"Bob <a@b.co>","age":18}`, "email", "email"},
	}
	for _, tc := range cases {
		rec := do(signupApp(), "POST", "/signup", tc.body)
		if tc.rule == "" {
			if rec.Code != 200 {
				t.Errorf("%s: status = %d, body = %s", tc.body, rec.Code, rec.Body)
			}
			continue
		}
		if rec.Code != 422 || !decodeErr(t, rec).has(tc.field, tc.rule) {
			t.Errorf("%s: want %s/%s, got %d %s", tc.body, tc.field, tc.rule, rec.Code, rec.Body)
		}
	}
}

func TestOptionalRulesOnlyRunWhenProvided(t *testing.T) {
	// role, website, token, code, tags, priority, zip are absent: no errors for them.
	rec := do(signupApp(), "POST", "/signup", `{"name":"abc","email":"a@b.co","age":18}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

type lineIn struct {
	Name string `json:"name" validate:"required"`
	Qty  *int   `json:"qty" validate:"required,min=1"`
}
type addressIn struct {
	City string `json:"city" validate:"required"`
}
type orderIn struct {
	Address addressIn            `json:"address"`
	Billing *addressIn           `json:"billing"`
	Lines   []lineIn             `json:"lines" validate:"min=1"`
	Extra   map[string]addressIn `json:"extra"`
}

func TestNestedValidationPaths(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/orders", func(*aemeath.Ctx, orderIn) (struct{}, error) { return struct{}{}, nil })
	rec := do(app, "POST", "/orders", `{
		"billing":{},
		"lines":[{"name":"ok","qty":1},{"qty":0}],
		"extra":{"home":{}}}`)
	e := decodeErr(t, rec)
	if rec.Code != 422 {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range [][2]string{
		{"address.city", "required"},     // a nested struct is validated even when omitted
		{"billing.city", "required"},     // pointer to struct, present
		{"lines[1].name", "required"},    // slice element
		{"lines[1].qty", "min"},          // pointer to zero, rule runs
		{"extra[home].city", "required"}, // map value
	} {
		if !e.has(want[0], want[1]) {
			t.Errorf("missing %v in %s", want, rec.Body)
		}
	}
	// A nil optional pointer to struct is fine.
	rec = do(app, "POST", "/orders", `{"address":{"city":"x"},"lines":[{"name":"a","qty":1}]}`)
	if rec.Code != 200 {
		t.Fatalf("valid order: status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestValidationErrorFromHandler(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/x", func(*aemeath.Ctx, struct{}) (struct{}, error) {
		return struct{}{}, aemeath.ValidationError(aemeath.FieldError{Field: "email", Rule: "unique", Message: "is already taken"})
	})
	rec := do(app, "POST", "/x", ``)
	if rec.Code != 422 || !decodeErr(t, rec).has("email", "unique") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

type paging struct {
	Page  int `query:"page"`
	Limit int `query:"limit" validate:"max=100"`
}
type listIn struct {
	paging
	Q string `query:"q"`
}

type secureIn struct {
	Token   string    `header:"X-Token" validate:"required,min=4"`
	Session string    `cookie:"session"`
	Since   time.Time `query:"since"`
	ID      string    `query:"id" validate:"uuid"`
}

func TestEmbeddedInputIsBoundAndValidated(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/l", func(_ *aemeath.Ctx, in listIn) (map[string]any, error) {
		return map[string]any{"page": in.Page, "limit": in.Limit, "q": in.Q}, nil
	})
	rec := get(app, "/l?page=2&limit=5&q=hi")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"limit":5,"page":2,"q":"hi"}` {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	rec = get(app, "/l?limit=500")
	if rec.Code != 422 || !decodeErr(t, rec).has("limit", "max") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestHeaderCookieAndTextParams(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/s", func(_ *aemeath.Ctx, in secureIn) (map[string]string, error) {
		return map[string]string{"token": in.Token, "session": in.Session, "since": in.Since.UTC().Format(time.RFC3339)}, nil
	})

	req := httptest.NewRequest("GET", "/s?since=2026-01-02T03:04:05Z", nil)
	req.Header.Set("X-Token", "abcd")
	req.AddCookie(&http.Cookie{Name: "session", Value: "s123"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	want := `{"session":"s123","since":"2026-01-02T03:04:05Z","token":"abcd"}`
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}

	if rec := get(app, "/s"); rec.Code != 422 || !decodeErr(t, rec).has("X-Token", "required") {
		t.Fatalf("missing header: status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := get(app, "/s?since=yesterday", "X-Token", "abcd"); rec.Code != 400 {
		t.Fatalf("bad time: status = %d, want 400", rec.Code)
	}
	if rec := get(app, "/s?id=zzz", "X-Token", "abcd"); rec.Code != 422 || !decodeErr(t, rec).has("id", "uuid") {
		t.Fatalf("bad uuid: status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestOpenAPIConstraintsComponentsAndSecurity(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/signup", func(_ *aemeath.Ctx, in signupIn) (orderIn, error) { return orderIn{}, nil },
		aemeath.RequireAuth(), aemeath.Description("Creates an account."), aemeath.Deprecated())
	doc := app.OpenAPI(aemeath.Info{Title: "t"})

	op := doc["paths"].(map[string]any)["/signup"].(map[string]any)["post"].(map[string]any)
	if op["deprecated"] != true || op["description"] != "Creates an account." || op["security"] == nil {
		t.Fatalf("operation metadata missing: %v", op)
	}
	responses := op["responses"].(map[string]any)
	for _, code := range []string{"200", "400", "401", "422", "500"} {
		if responses[code] == nil {
			t.Errorf("missing %s response", code)
		}
	}
	props := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
	name := props["name"].(map[string]any)
	if name["minLength"] != 3 || name["maxLength"] != 10 {
		t.Errorf("name constraints = %v", name)
	}
	if props["email"].(map[string]any)["format"] != "email" {
		t.Errorf("email = %v", props["email"])
	}
	age := props["age"].(map[string]any)
	if age["minimum"] != float64(18) || age["maximum"] != float64(120) || age["exclusiveMaximum"] != true {
		t.Errorf("age = %v", age)
	}
	if enum := props["role"].(map[string]any)["enum"].([]string); len(enum) != 3 {
		t.Errorf("role enum = %v", enum)
	}
	if props["tags"].(map[string]any)["maxItems"] != 2 {
		t.Errorf("tags = %v", props["tags"])
	}

	components := doc["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	for _, n := range []string{"Error", "ValidationError", "FieldError", "OrderIn", "LineIn", "AddressIn"} {
		if schemas[n] == nil {
			t.Errorf("components.schemas.%s is missing (have %d schemas)", n, len(schemas))
		}
	}
	if components["securitySchemes"].(map[string]any)["bearerAuth"] == nil {
		t.Error("bearerAuth security scheme missing")
	}
}

func TestOpenAPIRecursiveTypeUsesRef(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/c", func(_ *aemeath.Ctx, in category) (category, error) { return in, nil })
	schemas := app.OpenAPI(aemeath.Info{})["components"].(map[string]any)["schemas"].(map[string]any)
	cat := schemas["Category"].(map[string]any)["properties"].(map[string]any)
	if cat["parent"].(map[string]any)["allOf"] == nil {
		t.Fatalf("a nullable pointer to a struct should be allOf+$ref: %v", cat["parent"])
	}
}

func TestOpenAPIGroupOptions(t *testing.T) {
	app := aemeath.New()
	g := app.Group("/admin")
	g.Options(aemeath.Tags("admin"), aemeath.RequireAuth())
	aemeath.GroupGET(g, "/a", okRoute)
	aemeath.GroupGET(g, "/b", okRoute, aemeath.Tags("other"))
	paths := app.OpenAPI(aemeath.Info{})["paths"].(map[string]any)
	a := paths["/admin/a"].(map[string]any)["get"].(map[string]any)
	b := paths["/admin/b"].(map[string]any)["get"].(map[string]any)
	if a["tags"].([]string)[0] != "admin" || a["security"] == nil {
		t.Errorf("group options not applied: %v", a)
	}
	if b["tags"].([]string)[0] != "other" {
		t.Errorf("route options must win over group options: %v", b)
	}
}

func TestTypeScriptConstraintsUnionsAndErrors(t *testing.T) {
	type in struct {
		Role   string   `json:"role" validate:"required,oneof=admin user" description:"Who the user is"`
		Level  int      `json:"level" validate:"oneof=1 2 3"`
		Name   string   `json:"name" validate:"min=3,max=10" example:"Bob"`
		Tags   []string `query:"tag"`
		Token  string   `header:"X-Token" validate:"required"`
		Cookie string   `cookie:"session"`
	}
	app := aemeath.New()
	aemeath.POST(app, "/u", func(*aemeath.Ctx, in) (struct{}, error) { return struct{}{}, nil })
	aemeath.DELETE(app, "/u/{id}", func(*aemeath.Ctx, struct {
		ID string `path:"id"`
	}) (struct{}, error) {
		return struct{}{}, nil
	}, aemeath.Status(204))
	aemeath.GET(app, "/go", func(*aemeath.Ctx, struct{}) (aemeath.Handled, error) { return aemeath.Handled{}, nil })
	ts := app.TypeScriptClient()
	for _, want := range []string{
		`role: "admin" | "user"`,
		`level?: 1 | 2 | 3`,
		`/** @minLength 3 @maxLength 10 @example Bob */ name?: string`,
		`/** Who the user is */`,
		`headers: { `,
		`"X-Token": string`,
		`tag?: Array<string>`,
		`export class ApiError`,
		`public errors: FieldError[]`,
		`request<void>(opts, "DELETE"`,
		`request<unknown>(opts, "GET", "/go"`,
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("TypeScript output is missing %q\n%s", want, ts)
		}
	}
	if strings.Contains(ts, "session") {
		t.Error("cookie parameters must not appear in the browser client")
	}
}
