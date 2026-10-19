package aemeath

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// DefaultMaxBodyBytes adalah batas default ukuran body JSON.
var DefaultMaxBodyBytes int64 = 1 << 20 // 1 MiB

type routeConfig struct {
	summary string
	tags    []string
	status  int
	opID    string
}

// RouteOption mengatur metadata/perilaku sebuah route.
type RouteOption func(*routeConfig)

func OperationID(id string) RouteOption { return func(c *routeConfig) { c.opID = id } }

func Summary(s string) RouteOption { return func(c *routeConfig) { c.summary = s } }
func Tags(t ...string) RouteOption { return func(c *routeConfig) { c.tags = t } }
func Status(code int) RouteOption  { return func(c *routeConfig) { c.status = code } }

// Route mendaftarkan handler bertipe aman.
func Route[In, Out any](a *App, method, path string, fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	cfg := routeConfig{status: http.StatusOK}
	for _, o := range opts {
		o(&cfg)
	}
	validateInputType(reflect.TypeFor[In]())
	a.specs = append(a.specs, routeSpec{
		method: method, path: path,
		in: reflect.TypeFor[In](), out: reflect.TypeFor[Out](),
		cfg: cfg,
	})
	a.Handle(method, path, func(c *Ctx) error {
		var in In
		if err := bind(c, &in); err != nil {
			return err
		}
		out, err := fn(c, in)
		if err != nil {
			return err
		}
		return c.JSON(cfg.status, out)
	})
	return Endpoint[In, Out]{Method: method, Path: path}
}

func GET[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "GET", path, fn, o...)
}
func POST[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "POST", path, fn, o...)
}
func PUT[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "PUT", path, fn, o...)
}
func PATCH[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "PATCH", path, fn, o...)
}
func DELETE[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "DELETE", path, fn, o...)
}

func bodyMethod(m string) bool { return m == "POST" || m == "PUT" || m == "PATCH" }

func paramSource(f reflect.StructField) (string, string) {
	if n := f.Tag.Get("path"); n != "" {
		return "path", n
	}
	if n := f.Tag.Get("query"); n != "" {
		return "query", n
	}
	return "", ""
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

func hasRequired(f reflect.StructField) bool {
	for _, p := range strings.Split(f.Tag.Get("validate"), ",") {
		if p == "required" {
			return true
		}
	}
	return false
}

// validateInputType memastikan tipe In tidak mengandung kombinasi tag/tipe
// yang tidak bisa divalidasi dengan benar. Lebih baik panic saat startup
// daripada bug validasi silent di runtime.
//
// Aturan:
//   - Embedded struct (Anonymous) belum didukung → panic.
//   - Field scalar non-pointer (int, uint, float, bool) dengan `required`
//     tidak bisa membedakan "tidak dikirim" vs "dikirim nilai nol" → panic.
//     Gunakan pointer (mis. *int `validate:"required"`), atau hapus `required`.
//   - `required` bersama `json:",omitempty"` kontradiktif → panic.
//   - Nested struct diperiksa rekursif.
func validateInputType(t reflect.Type) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			panic(fmt.Sprintf(
				"aemeath: embedded field %s.%s belum didukung; gunakan field eksplisit",
				t.Name(), f.Name,
			))
		}
		if !f.IsExported() {
			continue
		}
		req := hasRequired(f)
		if req && hasOmitEmpty(f) {
			panic(fmt.Sprintf(
				"aemeath: field %s.%s punya `required` dan `omitempty` sekaligus — kontradiktif",
				t.Name(), f.Name,
			))
		}
		if req {
			ft := f.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if f.Type.Kind() != reflect.Ptr && ft.Kind() != reflect.String {
				panic(fmt.Sprintf(
					"aemeath: field %s.%s bertipe %s dengan `required` tidak bisa membedakan nilai kosong vs tidak dikirim; gunakan pointer (*%s) atau hapus `required`",
					t.Name(), f.Name, f.Type, f.Type,
				))
			}
		}
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			validateInputType(ft)
		}
	}
}

// bind mengisi struct dari body JSON, tag `path:"x"` dan `query:"x"`,
// lalu memvalidasi tag `validate:"required"` secara rekursif.
func bind(c *Ctx, in any) error {
	if bodyMethod(c.R.Method) && c.R.Body != nil {
		body, err := io.ReadAll(io.LimitReader(c.R.Body, DefaultMaxBodyBytes+1))
		if err != nil {
			return BadRequest("gagal membaca body")
		}
		if int64(len(body)) > DefaultMaxBodyBytes {
			return BadRequest(fmt.Sprintf("body melebihi batas %d byte", DefaultMaxBodyBytes))
		}
		if len(body) > 0 {
			// Tolak Content-Type yang jelas bukan JSON. Content-Type kosong
			// tetap diterima. `application/x-www-form-urlencoded` juga
			// dikecualikan agar curl default tidak ditolak, karena body-nya
			// tetap divalidasi sebagai JSON.
			ct := c.R.Header.Get("Content-Type")
			if ct != "" {
				lower := strings.ToLower(ct)
				if !strings.Contains(lower, "json") &&
					!strings.HasPrefix(lower, "application/x-www-form-urlencoded") {
					return NewError(http.StatusUnsupportedMediaType, "Content-Type harus application/json")
				}
			}
			if err := json.Unmarshal(body, in); err != nil {
				return BadRequest("body JSON tidak valid")
			}
		}
	}

	v := reflect.ValueOf(in).Elem()
	if v.Kind() != reflect.Struct {
		return nil
	}
	if err := bindParams(c, v); err != nil {
		return err
	}
	return validateRequired(v)
}

func bindParams(c *Ctx, v reflect.Value) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		src, name := paramSource(f)
		if src == "" {
			continue
		}
		fv := v.Field(i)
		fv.SetZero()
		var raw string
		if src == "path" {
			raw = c.R.PathValue(name)
		} else {
			raw = c.R.URL.Query().Get(name)
		}
		if raw == "" {
			continue
		}
		if err := setField(fv, raw); err != nil {
			return BadRequest(fmt.Sprintf("nilai %s '%s' tidak valid", src, name))
		}
	}
	return nil
}

// validateRequired memeriksa tag `validate:"required"` secara rekursif
// ke nested struct dan pointer ke struct.
func validateRequired(v reflect.Value) error {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		if hasRequired(f) && isAbsent(fv) {
			return BadRequest(jsonName(f) + " wajib diisi")
		}
		if err := validateRequired(fv); err != nil {
			return err
		}
	}
	return nil
}

// isAbsent menentukan "tidak dikirim" dengan sadar-tipe.
func isAbsent(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.String:
		return v.Len() == 0
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	}
	return v.IsZero()
}

func setField(fv reflect.Value, raw string) error {
	if fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return setField(fv.Elem(), raw)
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	default:
		return fmt.Errorf("tipe %s tidak didukung", fv.Kind())
	}
	return nil
}

// Register mendaftarkan handler untuk sebuah Endpoint.
func Register[In, Out any](a *App, e Endpoint[In, Out], fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	return Route(a, e.Method, e.Path, fn, opts...)
}
