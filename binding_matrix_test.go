package aemeath_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

// One struct per source, with every supported scalar type.
type qAll struct {
	I   int       `query:"i" json:"i"`
	I8  int8      `query:"i8" json:"i8"`
	I16 int16     `query:"i16" json:"i16"`
	I32 int32     `query:"i32" json:"i32"`
	I64 int64     `query:"i64" json:"i64,string"`
	U   uint      `query:"u" json:"u"`
	U8  uint8     `query:"u8" json:"u8"`
	U16 uint16    `query:"u16" json:"u16"`
	U32 uint32    `query:"u32" json:"u32"`
	U64 uint64    `query:"u64" json:"u64,string"`
	F32 float32   `query:"f32" json:"f32"`
	F64 float64   `query:"f64" json:"f64"`
	B   bool      `query:"b" json:"b"`
	S   string    `query:"s" json:"s"`
	P   *int      `query:"p" json:"p"`
	T   time.Time `query:"t" json:"t"`
}
type hAll struct {
	I   int       `header:"X-I" json:"i"`
	I8  int8      `header:"X-I8" json:"i8"`
	I16 int16     `header:"X-I16" json:"i16"`
	I32 int32     `header:"X-I32" json:"i32"`
	I64 int64     `header:"X-I64" json:"i64,string"`
	U   uint      `header:"X-U" json:"u"`
	U8  uint8     `header:"X-U8" json:"u8"`
	U16 uint16    `header:"X-U16" json:"u16"`
	U32 uint32    `header:"X-U32" json:"u32"`
	U64 uint64    `header:"X-U64" json:"u64,string"`
	F32 float32   `header:"X-F32" json:"f32"`
	F64 float64   `header:"X-F64" json:"f64"`
	B   bool      `header:"X-B" json:"b"`
	S   string    `header:"X-S" json:"s"`
	P   *int      `header:"X-P" json:"p"`
	T   time.Time `header:"X-T" json:"t"`
}
type cAll struct {
	I   int       `cookie:"i" json:"i"`
	I8  int8      `cookie:"i8" json:"i8"`
	I16 int16     `cookie:"i16" json:"i16"`
	I32 int32     `cookie:"i32" json:"i32"`
	I64 int64     `cookie:"i64" json:"i64,string"`
	U   uint      `cookie:"u" json:"u"`
	U8  uint8     `cookie:"u8" json:"u8"`
	U16 uint16    `cookie:"u16" json:"u16"`
	U32 uint32    `cookie:"u32" json:"u32"`
	U64 uint64    `cookie:"u64" json:"u64,string"`
	F32 float32   `cookie:"f32" json:"f32"`
	F64 float64   `cookie:"f64" json:"f64"`
	B   bool      `cookie:"b" json:"b"`
	S   string    `cookie:"s" json:"s"`
	P   *int      `cookie:"p" json:"p"`
	T   time.Time `cookie:"t" json:"t"`
}
type pAll struct {
	I   int       `path:"i" json:"i"`
	I8  int8      `path:"i8" json:"i8"`
	I16 int16     `path:"i16" json:"i16"`
	I32 int32     `path:"i32" json:"i32"`
	I64 int64     `path:"i64" json:"i64,string"`
	U   uint      `path:"u" json:"u"`
	U8  uint8     `path:"u8" json:"u8"`
	U16 uint16    `path:"u16" json:"u16"`
	U32 uint32    `path:"u32" json:"u32"`
	U64 uint64    `path:"u64" json:"u64,string"`
	F32 float32   `path:"f32" json:"f32"`
	F64 float64   `path:"f64" json:"f64"`
	B   bool      `path:"b" json:"b"`
	S   string    `path:"s" json:"s"`
	P   *int      `path:"p" json:"p"`
	T   time.Time `path:"t" json:"t"`
}

type bindCase struct {
	key, good, want string   // a valid value and how it reads back as JSON
	bad             []string // values that must answer 400
}

var bindCases = []bindCase{
	{"i", "-42", "-42", []string{"x", "1.5", "0x10", "9223372036854775808"}},
	{"i8", "-128", "-128", []string{"128", "-129", "x"}},
	{"i16", "32767", "32767", []string{"32768", "x"}},
	{"i32", "-2147483648", "-2147483648", []string{"2147483648", "x"}},
	{"i64", "9223372036854775807", `"9223372036854775807"`, []string{"9223372036854775808", "x"}},
	{"u", "42", "42", []string{"-1", "x", "18446744073709551616"}},
	{"u8", "255", "255", []string{"256", "-1"}},
	{"u16", "65535", "65535", []string{"65536"}},
	{"u32", "4294967295", "4294967295", []string{"4294967296"}},
	{"u64", "18446744073709551615", `"18446744073709551615"`, []string{"18446744073709551616", "-1"}},
	{"f32", "1.5", "1.5", []string{"x", "3.5e38", "NaN", "Inf"}},
	{"f64", "-0.25", "-0.25", []string{"x", "1e400", "NaN", "-Inf"}},
	{"b", "true", "true", []string{"yes", "2", "x"}},
	{"s", "héllo wörld", `"héllo wörld"`, nil},
	{"p", "7", "7", []string{"x"}},
	{"t", "2026-11-23T14:00:00+07:00", `"2026-11-23T14:00:00+07:00"`, []string{"2026-13-01T00:00:00Z", "yesterday"}},
}

// pathDefaults are valid values for the path segments not under test.
var pathDefaults = map[string]string{
	"i": "0", "i8": "0", "i16": "0", "i32": "0", "i64": "0", "u": "0", "u8": "0", "u16": "0", "u32": "0", "u64": "0",
	"f32": "0", "f64": "0", "b": "false", "s": "x", "p": "0", "t": "2026-01-01T00:00:00Z",
}

const pathPattern = "/p/{i}/{i8}/{i16}/{i32}/{i64}/{u}/{u8}/{u16}/{u32}/{u64}/{f32}/{f64}/{b}/{s}/{p}/{t}"

var pathOrder = []string{"i", "i8", "i16", "i32", "i64", "u", "u8", "u16", "u32", "u64", "f32", "f64", "b", "s", "p", "t"}

// Every supported scalar type, from every source: a valid value binds and
// reads back unchanged, an invalid or out-of-range one is refused with 400
// (never 500, never silently truncated), and an absent one is the zero value.
func TestBindingMatrix(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/q", func(c *aemeath.Ctx, in qAll) (qAll, error) { return in, nil })
	aemeath.GET(app, "/h", func(c *aemeath.Ctx, in hAll) (hAll, error) { return in, nil })
	aemeath.GET(app, "/c", func(c *aemeath.Ctx, in cAll) (cAll, error) { return in, nil })
	aemeath.GET(app, pathPattern, func(c *aemeath.Ctx, in pAll) (pAll, error) { return in, nil })

	request := func(source, key, value string) *http.Request {
		switch source {
		case "query":
			return httptest.NewRequest("GET", "/q?"+url.Values{key: {value}}.Encode(), nil)
		case "header":
			r := httptest.NewRequest("GET", "/h", nil)
			r.Header.Set("X-"+strings.ToUpper(key), value)
			return r
		case "cookie":
			// Sent as written: the binder does not unescape cookie values.
			r := httptest.NewRequest("GET", "/c", nil)
			r.Header.Set("Cookie", key+"="+value)
			return r
		default: // path
			segs := make([]string, len(pathOrder))
			for i, k := range pathOrder {
				v := pathDefaults[k]
				if k == key {
					v = value
				}
				segs[i] = url.PathEscape(v)
			}
			return httptest.NewRequest("GET", "/p/"+strings.Join(segs, "/"), nil)
		}
	}

	for _, source := range []string{"path", "query", "header", "cookie"} {
		for _, bc := range bindCases {
			name := fmt.Sprintf("%s/%s", source, bc.key)
			if source == "cookie" && bc.key == "s" {
				// A cookie value cannot hold spaces or non-ASCII text (RFC 6265):
				// net/http drops such a cookie before the binder sees it.
				bc = bindCase{key: "s", good: "hello_world-1", want: `"hello_world-1"`}
			}
			w := httptest.NewRecorder()
			app.ServeHTTP(w, request(source, bc.key, bc.good))
			if w.Code != http.StatusOK {
				t.Errorf("%s = %q -> %d %s, want 200", name, bc.good, w.Code, w.Body.String())
				continue
			}
			var got map[string]json.RawMessage
			_ = json.Unmarshal(w.Body.Bytes(), &got)
			if string(got[bc.key]) != bc.want {
				t.Errorf("%s = %q reads back as %s, want %s", name, bc.good, got[bc.key], bc.want)
			}
			for _, bad := range bc.bad {
				w := httptest.NewRecorder()
				app.ServeHTTP(w, request(source, bc.key, bad))
				if w.Code != http.StatusBadRequest {
					t.Errorf("%s = %q -> %d %s, want 400", name, bad, w.Code, w.Body.String())
				}
			}
		}
	}

	// Absent parameters (query, header, cookie) are the zero value, not an error.
	for _, path := range []string{"/q", "/h", "/c"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"i":0`) || !strings.Contains(w.Body.String(), `"p":null`) {
			t.Errorf("GET %s without parameters -> %d %s, want 200 with zero values", path, w.Code, w.Body.String())
		}
	}
}
