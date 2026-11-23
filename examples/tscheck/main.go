// Command tscheck is the server used by CI to verify the generated TypeScript:
//
//	go run ./examples/tscheck -write examples/tscheck/generated   # write client.ts, schemas.ts, hooks.ts
//	go run ./examples/tscheck                                     # serve on :38123
//
// examples/tscheck/verify.ts then type-checks the generated files and checks
// that the generated Zod schemas make the same accept/reject decisions as the server.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"time"

	"github.com/naufalraism/aemeath"
)

type Address struct {
	City string `json:"city" validate:"required,min=2"`
}
type SignupIn struct {
	Name     string   `json:"name" validate:"required,min=3,max=10" description:"Display name" example:"Bob"`
	Email    string   `json:"email" validate:"required,email"`
	Role     string   `json:"role" validate:"oneof=admin user"`
	Age      *int     `json:"age" validate:"required,gte=18,lt=120"`
	Level    int      `json:"level" validate:"oneof=1 2 3"`
	Tags     []string `json:"tags" validate:"max=2"`
	Code     string   `json:"code" validate:"pattern=^[A-Z]{3}-[0-9]+$"`
	Address  Address  `json:"address"`
	Nickname *string  `json:"nickname"`
}
type User struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	Nickname *string  `json:"nickname"`
	Friends  []*User  `json:"friends,omitempty"`
}
type ListIn struct {
	Tags  []string `query:"tag" validate:"max=3"`
	Limit *int     `query:"limit" validate:"max=100"`
	Trace string   `header:"X-Trace"`
}
type ByID struct {
	ID int `path:"id"`
}

// ---- wire format: types whose JSON form differs from what their Go fields
// suggest. GET /wire/zero and /wire/full return a zero and a filled value;
// verify.ts checks both against the generated Zod schema.

type Status string

type Money struct{ Cents int64 }

func (m Money) MarshalJSON() ([]byte, error) { return []byte(`"12.50"`), nil }

type Level int

func (l Level) MarshalText() ([]byte, error) { return []byte("high"), nil }
func (l *Level) UnmarshalText([]byte) error  { *l = 1; return nil }

type Stamps struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}
type Meta struct {
	Note string `json:"note"`
}
type Child struct {
	Name string `json:"name" validate:"required,min=2"`
}
type Wire struct {
	Stamps                    // embedded struct: fields are promoted
	*Meta                     // embedded pointer: absent when nil
	Named     Meta            `json:"named"`
	Ptr       *string         `json:"ptr"`
	PtrOmit   *string         `json:"ptr_omit,omitempty"`
	StrOmit   string          `json:"str_omit,omitempty"`
	Slice     []string        `json:"slice"`
	Map       map[string]int  `json:"map"`
	IntKeys   map[int]string  `json:"int_keys"`
	Bytes     []byte          `json:"bytes"`
	Array     [2]int          `json:"array"`
	Status    Status          `json:"status"`
	Money     Money           `json:"money"`
	Level     Level           `json:"level"`
	Duration  time.Duration   `json:"duration"`
	Raw       json.RawMessage `json:"raw"`
	Any       any             `json:"any"`
	Quoted    uint64          `json:"quoted,string"`
	Untagged  string
	Hidden    string                    `json:"-"`
	Children  []Child                   `json:"children"`
	ChildPtrs []*Child                  `json:"child_ptrs"`
	PtrChild  *Child                    `json:"ptr_child"`
	When      *time.Time                `json:"when"`
	Zero      time.Time                 `json:"zero,omitzero"`
	Nested    map[string][]Child        `json:"nested"`
	Deep      []map[string][]*time.Time `json:"deep"`
}

// ---- strict input: every rule, on a field that is NOT required, so the
// "was a value sent?" rule is exercised (see docs/guide/validation.md).

type StrictIn struct {
	Name     string    `json:"name" validate:"required,min=2,max=5"`
	Short    string    `json:"short" validate:"max=3"`
	Age      *int      `json:"age" validate:"required,gte=1"`
	Flag     *bool     `json:"flag" validate:"required"`
	OptPtr   *int      `json:"opt_ptr" validate:"min=1"`
	Tags     []string  `json:"tags" validate:"min=1,max=2"`
	Kind     int       `json:"kind" validate:"oneof=1 2"`
	Status   Status    `json:"status" validate:"oneof=a b"`
	Email    string    `json:"email" validate:"email"`
	URL      string    `json:"url" validate:"url"`
	UUID     string    `json:"uuid" validate:"uuid"`
	Ratio    float64   `json:"ratio" validate:"gt=0,lt=1"`
	Children []Child   `json:"children"`
	When     time.Time `json:"when"`
	Omit     string    `json:"omit,omitempty" validate:"min=3"`
	CodeI    string    `json:"code_i" validate:"pattern=(?i)^[a-z]{2,3}$"`
	CodeN    string    `json:"code_n" validate:"pattern=(?P<p>\\d{1,3})-x"`
	CodeA    string    `json:"code_a" validate:"pattern=\\Aab\\z"`
}

func main() {
	write := flag.String("write", "", "write the generated TypeScript into this directory and exit")
	flag.Parse()

	app := newApp()
	if *write != "" {
		if err := app.WriteTypeScript(*write); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := app.Listen(":38123"); err != nil {
		log.Fatal(err)
	}
}

// docsInfo is the OpenAPI info of this app (also used by the golden test).
var docsInfo = aemeath.Info{Title: "TypeScript check"}

// newApp registers every route. golden_test.go compares what the generators
// produce for it with the files in testdata/golden.
func newApp() *aemeath.App {
	app := aemeath.New()
	app.Use(aemeath.RequestID(), aemeath.Logger(), aemeath.CORS())
	aemeath.GET(app, "/items", func(c *aemeath.Ctx, in ListIn) ([]User, error) { return []User{{ID: 1, Tags: in.Tags}}, nil })
	aemeath.POST(app, "/signup", func(c *aemeath.Ctx, in SignupIn) (User, error) { return User{ID: 7, Name: in.Name}, nil }, aemeath.Status(201))
	aemeath.GET(app, "/items/{id}", func(c *aemeath.Ctx, in ByID) (User, error) { return User{ID: in.ID}, nil })
	aemeath.DELETE(app, "/items/{id}", func(c *aemeath.Ctx, in ByID) (struct{}, error) { return struct{}{}, nil }, aemeath.Status(204))
	aemeath.GET(app, "/ping", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) { return map[string]string{"ok": "1"}, nil })
	aemeath.GET(app, "/wire/zero", func(c *aemeath.Ctx, _ struct{}) (Wire, error) { return Wire{}, nil })
	aemeath.GET(app, "/wire/full", func(c *aemeath.Ctx, _ struct{}) (Wire, error) {
		s := "x"
		now := time.Date(2026, 11, 16, 22, 0, 0, 123456789, time.FixedZone("WIB", 7*3600))
		return Wire{
			Stamps: Stamps{ID: 1, CreatedAt: now}, Meta: &Meta{Note: "n"}, Ptr: &s, PtrOmit: &s, StrOmit: "x",
			Slice: []string{"a"}, Map: map[string]int{"a": 1}, IntKeys: map[int]string{1: "a"}, Bytes: []byte("hi"),
			Status: "a", Duration: time.Second, Raw: json.RawMessage(`{"k":[1,null]}`), Any: 1.5, Quoted: 1 << 62,
			Children: []Child{{Name: "ab"}}, ChildPtrs: []*Child{nil, {Name: "cd"}}, PtrChild: &Child{Name: "ef"},
			When: &now, Zero: now, Nested: map[string][]Child{"a": nil, "b": {{Name: "gh"}}},
			Deep: []map[string][]*time.Time{nil, {"k": {nil, &now}}},
		}, nil
	})
	aemeath.POST(app, "/strict", func(c *aemeath.Ctx, in StrictIn) (map[string]bool, error) { return map[string]bool{"ok": true}, nil })
	app.EnableClient()
	app.EnableDocs(docsInfo)
	return app
}
