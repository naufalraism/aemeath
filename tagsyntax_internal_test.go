package aemeath

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// A tag Go cannot read (here `\d`, as an earlier version of the validation
// guide showed) used to drop every rule of the field silently: the field was
// neither required nor checked, and the Zod schema and OpenAPI lost the rules
// too. Registration now refuses it. The types are built at run time because
// go vet rejects such a tag in source, which is the point.
func TestUnreadableTagIsRefused(t *testing.T) {
	for _, tc := range []struct {
		tag, key string
	}{
		{`json:"zip" validate:"required,pattern=^\d{5}$"`, "validate"},
		{`validate:"required,pattern=^\d{5}$" json:"zip"`, "validate"},
		{`json:"z\ip" validate:"required"`, "json"},
		{`json:"zip" validate:required`, "validate"},
		{`json:"zip" validate:"required`, "validate"},
		{`query:"q\w"`, "query"},
	} {
		typ := reflect.StructOf([]reflect.StructField{{Name: "Zip", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(tc.tag)}})
		msg := registerPanics(typ)
		if !strings.Contains(msg, "cannot be read") || !strings.Contains(msg, "`"+tc.key+"`") {
			t.Errorf("tag %s: panic %q, want one naming %q", tc.tag, msg, tc.key)
		}
	}
	// Escapes that are valid Go are fine, and so is a bad escape in a tag
	// Aemeath does not read (reflect skips it when looking up other keys).
	for _, tag := range []string{
		`json:"zip" validate:"required,pattern=^\\d{5}$"`,
		`json:"zip" validate:"required,pattern=^[0-9]{5}$"`,
		`json:"zip" gorm:"check:\d" validate:"required"`,
	} {
		typ := reflect.StructOf([]reflect.StructField{{Name: "Zip", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(tag)}})
		if msg := registerPanics(typ); msg != "" {
			t.Errorf("tag %s: unexpected panic %q", tag, msg)
		}
	}
}

func registerPanics(typ reflect.Type) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	app := New()
	registerRuntimeRoute(app, "POST", "/x", typ, typ)
	return ""
}
