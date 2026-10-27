package aemeath

import (
	"fmt"
	"html"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Info is the metadata of the OpenAPI document.
type Info struct {
	Title       string
	Version     string
	Description string
}

type routeSpec struct {
	method, path string
	in, out      reflect.Type
	cfg          routeConfig
}

// reservedSchemaNames are component names owned by Aemeath itself.
var reservedSchemaNames = []string{"Error", "ValidationError", "FieldError"}

// OpenAPI builds an OpenAPI 3.0 document from all typed routes.
//
// Named structs become reusable entries in components.schemas (referenced
// with $ref), so recursive types work. Types used as request input are named
// "<Type>Input" because their requiredness and constraints differ from the
// same type used in a response.
func (a *App) OpenAPI(info Info) map[string]any {
	if info.Version == "" {
		info.Version = "0.1.0"
	}
	i := map[string]any{"title": info.Title, "version": info.Version}
	if info.Description != "" {
		i["description"] = info.Description
	}

	sb := newSchemaBuilder()
	paths := map[string]any{}
	secured := false
	for _, s := range a.specs {
		p := openAPIPath(s.path)
		item, _ := paths[p].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[p] = item
		}
		item[strings.ToLower(s.method)] = s.operation(sb)
		secured = secured || s.cfg.secure
	}

	schemas := sb.schemas
	schemas["Error"] = map[string]any{
		"type":       "object",
		"properties": map[string]any{"error": map[string]any{"type": "string"}},
		"required":   []string{"error"},
	}
	schemas["FieldError"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field":   map[string]any{"type": "string", "description": "Name of the invalid field, e.g. address.city or items[0].name"},
			"rule":    map[string]any{"type": "string", "description": "The validation rule that failed"},
			"message": map[string]any{"type": "string"},
		},
		"required": []string{"field", "rule", "message"},
	}
	schemas["ValidationError"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"error":  map[string]any{"type": "string"},
			"errors": map[string]any{"type": "array", "items": schemaRef("FieldError")},
		},
		"required": []string{"error", "errors"},
	}
	components := map[string]any{"schemas": schemas}
	if secured {
		components["securitySchemes"] = map[string]any{
			"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
		}
	}
	return map[string]any{"openapi": "3.0.3", "info": i, "paths": paths, "components": components}
}

// EnableDocs registers GET /openapi.json and GET /docs.
func (a *App) EnableDocs(info Info) {
	a.Handle("GET", "/openapi.json", func(c *Ctx) error {
		return c.JSON(200, a.OpenAPI(info))
	})
	a.Handle("GET", "/docs", func(c *Ctx) error {
		c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := io.WriteString(c.W, fmt.Sprintf(docsHTML, html.EscapeString(info.Title)))
		return err
	})
}

const docsHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>%s</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body>
<script id="api-reference" data-url="/openapi.json"></script>
<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body></html>`

var pathParamRe = regexp.MustCompile(`\{([^}.$]+)(?:\.\.\.)?\}`)

func openAPIPath(p string) string {
	p = strings.TrimSuffix(p, "{$}")
	return strings.ReplaceAll(p, "...}", "}")
}

func jsonContent(schema map[string]any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}

func schemaRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func (s routeSpec) operation(sb *schemaBuilder) map[string]any {
	op := map[string]any{}
	op["operationId"] = s.opID()
	if s.cfg.summary != "" {
		op["summary"] = s.cfg.summary
	}
	if s.cfg.description != "" {
		op["description"] = s.cfg.description
	}
	if len(s.cfg.tags) > 0 {
		op["tags"] = s.cfg.tags
	}
	if s.cfg.deprecated {
		op["deprecated"] = true
	}
	if s.cfg.secure {
		op["security"] = []any{map[string]any{"bearerAuth": []any{}}}
	}

	params := []any{}
	declared := map[string]bool{}
	t := s.in
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	hasBody := false
	if t.Kind() == reflect.Struct {
		for _, f := range flatFields(t) {
			src, name := paramSource(f.StructField)
			if src == "" {
				continue
			}
			schema := sb.annotate(sb.schemaOf(f.Type, true), f.StructField, true)
			params = append(params, map[string]any{
				"name": name, "in": src,
				"required": src == "path" || hasRequired(f.StructField),
				"schema":   schema,
			})
			declared[src+":"+name] = true
		}
		if bodyMethod(s.method) {
			body := sb.structSchema(t, true, true)
			if len(body["properties"].(map[string]any)) > 0 {
				hasBody = true
				op["requestBody"] = map[string]any{
					"required": body["required"] != nil,
					"content":  jsonContent(body),
				}
			}
		}
	}
	// Path params present in the pattern but not declared in the input struct.
	for _, m := range pathParamRe.FindAllStringSubmatch(s.path, -1) {
		if !declared["path:"+m[1]] {
			params = append(params, map[string]any{
				"name": m[1], "in": "path", "required": true,
				"schema": map[string]any{"type": "string"},
			})
		}
	}
	if len(params) > 0 {
		op["parameters"] = params
	}

	success := map[string]any{"description": "Success"}
	if bodyAllowed(s.cfg.status) && s.out != reflect.TypeFor[Handled]() {
		success["content"] = jsonContent(sb.schemaOf(s.out, false))
	}
	responses := map[string]any{
		strconv.Itoa(s.cfg.status): success,
		"500":                      map[string]any{"description": "Internal server error", "content": jsonContent(schemaRef("Error"))},
	}
	if len(params) > 0 || hasBody {
		responses["400"] = map[string]any{"description": "Malformed request (for example an invalid JSON body or parameter)", "content": jsonContent(schemaRef("Error"))}
		responses["422"] = map[string]any{"description": "Validation failed", "content": jsonContent(schemaRef("ValidationError"))}
	}
	if s.cfg.secure {
		responses["401"] = map[string]any{"description": "Missing or invalid token", "content": jsonContent(schemaRef("Error"))}
	}
	op["responses"] = responses
	return op
}

var timeType = reflect.TypeFor[time.Time]()

const maxSchemaDepth = 8

type nameKey struct {
	t     reflect.Type
	input bool
}

type schemaBuilder struct {
	schemas map[string]any
	names   map[nameKey]string
	owner   map[string]nameKey
}

func newSchemaBuilder() *schemaBuilder {
	sb := &schemaBuilder{
		schemas: map[string]any{},
		names:   map[nameKey]string{},
		owner:   map[string]nameKey{},
	}
	for _, n := range reservedSchemaNames {
		sb.owner[n] = nameKey{}
	}
	return sb
}

var nonNameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func (sb *schemaBuilder) nameFor(t reflect.Type, input bool) string {
	key := nameKey{t, input}
	if n, ok := sb.names[key]; ok {
		return n
	}
	base := upperFirst(strings.Trim(nonNameChars.ReplaceAllString(t.Name(), "_"), "_"))
	if base == "" {
		base = "Anonymous"
	}
	if input {
		base += "Input"
	}
	name := base
	for i := 2; ; i++ {
		if o, taken := sb.owner[name]; !taken || o == key {
			break
		}
		name = base + strconv.Itoa(i)
	}
	sb.names[key] = name
	sb.owner[name] = key
	return name
}

// ref registers a named struct in components.schemas and returns a $ref to it.
func (sb *schemaBuilder) ref(t reflect.Type, input bool) map[string]any {
	key := nameKey{t, input}
	_, known := sb.names[key]
	name := sb.nameFor(t, input)
	if !known {
		sb.schemas[name] = map[string]any{} // reserved first so recursive types terminate
		sb.schemas[name] = sb.structSchema(t, input, false)
	}
	return schemaRef(name)
}

func (sb *schemaBuilder) schemaOf(t reflect.Type, input bool) map[string]any {
	ptr := false
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
		ptr = true
	}
	var s map[string]any
	switch {
	case t == timeType:
		s = map[string]any{"type": "string", "format": "date-time"}
	case isTextType(t):
		s = map[string]any{"type": "string"}
		if t.Name() == "UUID" {
			s["format"] = "uuid"
		}
	default:
		s = sb.kindSchema(t, input)
	}
	if ptr && !input {
		s = withNull(s, true)
	}
	return s
}

func (sb *schemaBuilder) kindSchema(t reflect.Type, input bool) map[string]any {
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return withNull(map[string]any{"type": "array", "items": sb.schemaOf(t.Elem(), input)}, !input && t.Kind() == reflect.Slice)
	case reflect.Map:
		return withNull(map[string]any{"type": "object", "additionalProperties": sb.schemaOf(t.Elem(), input)}, !input)
	case reflect.Struct:
		if t.Name() != "" {
			return sb.ref(t, input)
		}
		return sb.structSchema(t, input, false)
	}
	return map[string]any{}
}

func (sb *schemaBuilder) structSchema(t reflect.Type, input bool, skipParams bool) map[string]any {
	props := map[string]any{}
	var required []string
	for _, f := range jsonFields(t) {
		if skipParams {
			if src, _ := paramSource(f); src != "" {
				continue
			}
		}
		name := jsonName(f)
		if name == "-" {
			continue
		}
		props[name] = sb.annotate(sb.schemaOf(f.Type, input), f, input)

		// Requiredness depends on direction: input uses `validate:"required"`,
		// output uses the absence of `omitempty`.
		if input {
			if inputRequired(f) {
				required = append(required, name)
			}
		} else if !hasOmitEmpty(f) {
			required = append(required, name)
		}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// annotate adds documentation (`description` and `example` tags) and, for
// input, the constraints of the field's `validate` rules to a schema.
func (sb *schemaBuilder) annotate(schema map[string]any, f reflect.StructField, input bool) map[string]any {
	extras := map[string]any{}
	if d := f.Tag.Get("description"); d != "" {
		extras["description"] = d
	}
	if ex := f.Tag.Get("example"); ex != "" {
		extras["example"] = exampleValue(f.Type, ex)
	}
	if input {
		for k, v := range ruleConstraints(f) {
			extras[k] = v
		}
	}
	if len(extras) == 0 {
		return schema
	}
	if _, isRef := schema["$ref"]; isRef {
		extras["allOf"] = []any{schema}
		return extras
	}
	for k, v := range extras {
		schema[k] = v
	}
	return schema
}

func exampleValue(t reflect.Type, raw string) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch classOf(t) {
	case kindNumber:
		if n, err := strconv.ParseFloat(raw, 64); err == nil {
			if t.Kind() != reflect.Float32 && t.Kind() != reflect.Float64 && n == float64(int64(n)) {
				return int64(n)
			}
			return n
		}
	case kindOther:
		if t.Kind() == reflect.Bool {
			if b, err := strconv.ParseBool(raw); err == nil {
				return b
			}
		}
	}
	return raw
}

// ruleConstraints maps `validate` rules to OpenAPI schema keywords.
func ruleConstraints(f reflect.StructField) map[string]any {
	_, rules := parseRules(reflect.TypeOf(struct{}{}), f)
	out := map[string]any{}
	class := classOf(f.Type)
	base := f.Type
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	isMap := base.Kind() == reflect.Map
	for _, r := range rules {
		switch class {
		case kindString:
			switch r.name {
			case "min":
				out["minLength"] = int(r.num)
			case "max":
				out["maxLength"] = int(r.num)
			case "len":
				out["minLength"], out["maxLength"] = int(r.num), int(r.num)
			case "oneof":
				out["enum"] = r.set
			case "email":
				out["format"] = "email"
			case "url":
				out["format"] = "uri"
			case "uuid":
				out["format"] = "uuid"
			case "pattern":
				out["pattern"] = r.arg
			}
		case kindNumber:
			switch r.name {
			case "min", "gte":
				out["minimum"] = r.num
			case "gt":
				out["minimum"], out["exclusiveMinimum"] = r.num, true
			case "max", "lte":
				out["maximum"] = r.num
			case "lt":
				out["maximum"], out["exclusiveMaximum"] = r.num, true
			case "oneof":
				out["enum"] = r.nums
			}
		case kindList:
			lo, hi := "minItems", "maxItems"
			if isMap {
				lo, hi = "minProperties", "maxProperties"
			}
			switch r.name {
			case "min":
				out[lo] = int(r.num)
			case "max":
				out[hi] = int(r.num)
			case "len":
				out[lo], out[hi] = int(r.num), int(r.num)
			}
		}
	}
	return out
}

// withNull marks a schema nullable: a nil pointer, slice or map is encoded as JSON null in responses.
func withNull(schema map[string]any, nullable bool) map[string]any {
	if !nullable {
		return schema
	}
	if _, isRef := schema["$ref"]; isRef {
		return map[string]any{"nullable": true, "allOf": []any{schema}}
	}
	schema["nullable"] = true
	return schema
}
