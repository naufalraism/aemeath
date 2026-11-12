package aemeath

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// DefaultMaxMultipartBytes is the default limit for a whole multipart/form-data
// request body (32 MiB). Urlencoded forms use DefaultMaxBodyBytes. Both can be
// changed before the server starts.
var DefaultMaxMultipartBytes int64 = 32 << 20

// formMemoryBytes is how much of a multipart body is kept in memory before
// spilling to temporary files.
const formMemoryBytes = 8 << 20

var (
	fileHeaderPtrType   = reflect.TypeFor[*multipart.FileHeader]()
	fileHeaderSliceType = reflect.TypeFor[[]*multipart.FileHeader]()
)

// formName returns the `form` tag name of a field, or "".
func formName(f reflect.StructField) string {
	n, _, _ := strings.Cut(f.Tag.Get("form"), ",")
	if n == "-" {
		return ""
	}
	return n
}

func isFileType(t reflect.Type) bool { return t == fileHeaderPtrType || t == fileHeaderSliceType }

// hasForm reports whether any field of the input is bound from a form.
func (i *inputInfo) hasForm() bool { return len(i.forms) > 0 }

// hasFileField reports whether the form has a file field (multipart is then required).
func (i *inputInfo) hasFileField() bool {
	for _, f := range i.forms {
		if isFileType(f.Type) {
			return true
		}
	}
	return false
}

// parseSize parses "512", "10KB", "5MB", "1GB" (powers of 1024).
func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suf) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suf)), u.m
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size")
	}
	return n * mult, nil
}

// checkFormTags panics at startup for form tags that cannot work.
func checkFormTags(t reflect.Type, f reflect.StructField) {
	name := formName(f)
	if name == "" {
		return
	}
	if src, _ := paramSource(f); src != "" {
		panic(fmt.Sprintf("aemeath: field %s.%s has both `form` and `%s` tags", t.Name(), f.Name, src))
	}
	if ms := f.Tag.Get("maxsize"); ms != "" {
		if !isFileType(f.Type) {
			panic(fmt.Sprintf("aemeath: field %s.%s: `maxsize` only applies to file fields", t.Name(), f.Name))
		}
		if _, err := parseSize(ms); err != nil {
			panic(fmt.Sprintf("aemeath: field %s.%s: invalid maxsize %q (use bytes or KB/MB/GB)", t.Name(), f.Name, ms))
		}
	}
	if f.Tag.Get("accept") != "" && !isFileType(f.Type) {
		panic(fmt.Sprintf("aemeath: field %s.%s: `accept` only applies to file fields", t.Name(), f.Name))
	}
}

// bindForm reads an urlencoded or multipart body into the form-tagged fields.
// File content types are detected from the bytes, never taken from the client.
func bindForm(c *Ctx, v reflect.Value, info *inputInfo) (cleanup func(), errs []FieldError, err error) {
	ct := c.R.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	limit := DefaultMaxBodyBytes
	switch mt {
	case "multipart/form-data":
		limit = DefaultMaxMultipartBytes
	case "application/x-www-form-urlencoded":
		if info.hasFileField() {
			return nil, nil, UnsupportedMediaType("Content-Type must be multipart/form-data")
		}
	default:
		if info.hasFileField() {
			return nil, nil, UnsupportedMediaType("Content-Type must be multipart/form-data")
		}
		return nil, nil, UnsupportedMediaType("Content-Type must be multipart/form-data or application/x-www-form-urlencoded")
	}
	c.R.Body = http.MaxBytesReader(c.W, c.R.Body, limit)
	var perr error
	if mt == "multipart/form-data" {
		perr = c.R.ParseMultipartForm(formMemoryBytes)
	} else {
		perr = c.R.ParseForm()
	}
	cleanup = func() {
		if c.R.MultipartForm != nil {
			_ = c.R.MultipartForm.RemoveAll()
		}
	}
	if perr != nil {
		cleanup()
		var mbe *http.MaxBytesError
		if errors.As(perr, &mbe) {
			return nil, nil, PayloadTooLarge(fmt.Sprintf("request body exceeds the limit of %d bytes", mbe.Limit))
		}
		return nil, nil, BadRequest("invalid form body")
	}

	for _, ff := range info.forms {
		name := formName(ff.StructField)
		fv, _ := fieldByPath(v, ff.Path, true)
		fv.SetZero()

		if isFileType(ff.Type) {
			var files []*multipart.FileHeader
			if c.R.MultipartForm != nil {
				files = c.R.MultipartForm.File[name]
			}
			if len(files) == 0 {
				continue
			}
			if ff.Type == fileHeaderPtrType {
				if len(files) > 1 {
					errs = append(errs, FieldError{Field: name, Rule: "single", Message: "accepts exactly one file"})
					fv.Set(reflect.ValueOf(files[0])) // keeps `required` from also firing
					continue
				}
				fv.Set(reflect.ValueOf(files[0]))
			} else {
				fv.Set(reflect.ValueOf(files))
			}
			errs = append(errs, checkFiles(name, ff.StructField, files)...)
			continue
		}

		vals := c.R.PostForm[name] // body only, never the URL query
		if c.R.MultipartForm != nil {
			vals = c.R.MultipartForm.Value[name]
		}
		if len(vals) == 0 {
			continue
		}
		t := ff.Type
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 && !isTextType(t) {
			if fv.Kind() == reflect.Pointer {
				fv.Set(reflect.New(fv.Type().Elem()))
				fv = fv.Elem()
			}
			sl := reflect.MakeSlice(fv.Type(), len(vals), len(vals))
			for j, raw := range vals {
				if e := setField(sl.Index(j), raw); e != nil {
					cleanup()
					return nil, nil, BadRequest(fmt.Sprintf("invalid value for form field '%s'", name))
				}
			}
			fv.Set(sl)
			continue
		}
		if vals[0] == "" {
			continue
		}
		if e := setField(fv, vals[0]); e != nil {
			cleanup()
			return nil, nil, BadRequest(fmt.Sprintf("invalid value for form field '%s'", name))
		}
	}
	return cleanup, errs, nil
}

// checkFiles enforces `maxsize` and `accept` on uploaded files.
func checkFiles(name string, f reflect.StructField, files []*multipart.FileHeader) []FieldError {
	var errs []FieldError
	max := int64(-1)
	if ms := f.Tag.Get("maxsize"); ms != "" {
		max, _ = parseSize(ms)
	}
	var accept []string
	for _, a := range strings.Split(f.Tag.Get("accept"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			accept = append(accept, strings.ToLower(a))
		}
	}
	for _, fh := range files {
		if max >= 0 && fh.Size > max {
			errs = append(errs, FieldError{Field: name, Rule: "maxsize", Message: fmt.Sprintf("file %q is larger than %s", safeName(fh.Filename), f.Tag.Get("maxsize"))})
			continue
		}
		if len(accept) == 0 {
			continue
		}
		got, e := sniff(fh)
		if e != nil || !acceptsType(accept, got) {
			errs = append(errs, FieldError{Field: name, Rule: "accept", Message: fmt.Sprintf("file %q has type %s; accepted: %s", safeName(fh.Filename), got, strings.Join(accept, ", "))})
		}
	}
	return errs
}

func sniff(fh *multipart.FileHeader) (string, error) {
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	mt, _, _ := mime.ParseMediaType(http.DetectContentType(buf[:n]))
	return mt, nil
}

func acceptsType(accept []string, got string) bool {
	for _, a := range accept {
		if a == got || (strings.HasSuffix(a, "/*") && strings.HasPrefix(got, strings.TrimSuffix(a, "*"))) {
			return true
		}
	}
	return false
}

// safeName strips control characters so a file name cannot forge log or error text.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// bindWithForm is bind for inputs that have form-tagged fields.
func bindWithForm(c *Ctx, in any, info *inputInfo) error {
	v := reflect.ValueOf(in).Elem()
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	var formErrs []FieldError
	if c.R.Body != nil && c.R.Body != http.NoBody {
		cleanup, errs, err := bindForm(c, v, info)
		if err != nil {
			return err
		}
		c.cleanups = append(c.cleanups, cleanup)
		formErrs = errs
	}
	if err := bindParams(c, v, info); err != nil {
		return err
	}
	errs := formErrs
	info.plan.validate(v, "", &errs)
	if len(errs) > 0 {
		return ValidationError(errs...)
	}
	return nil
}

func (c *Ctx) runCleanups() {
	for _, f := range c.cleanups {
		f()
	}
}
