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

// Info adalah metadata dokumen OpenAPI.
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

// OpenAPI membangun dokumen OpenAPI 3.0 dari semua route bertipe.
func (a *App) OpenAPI(info Info) map[string]any {
	if info.Version == "" {
		info.Version = "0.1.0"
	}
	i := map[string]any{"title": info.Title, "version": info.Version}
	if info.Description != "" {
		i["description"] = info.Description
	}
	paths := map[string]any{}
	for _, s := range a.specs {
		p := openAPIPath(s.path)
		item, _ := paths[p].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[p] = item
		}
		item[strings.ToLower(s.method)] = s.operation()
	}
	return map[string]any{"openapi": "3.0.3", "info": i, "paths": paths}
}

// EnableDocs mendaftarkan GET /openapi.json dan GET /docs.
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

func (s routeSpec) operation() map[string]any {
	op := map[string]any{}
	op["operationId"] = s.opID()
	if s.cfg.summary != "" {
		op["summary"] = s.cfg.summary
	}
	if len(s.cfg.tags) > 0 {
		op["tags"] = s.cfg.tags
	}

	params := []any{}
	declared := map[string]bool{}
	t := s.in
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			src, name := paramSource(f)
			if src == "" {
				continue
			}
			params = append(params, map[string]any{
				"name": name, "in": src,
				"required": src == "path" || hasRequired(f),
				"schema":   schemaOf(f.Type, 1, true), // FIX: input context
			})
			declared[src+":"+name] = true
		}
		if bodyMethod(s.method) {
			body := structSchema(t, 0, true, true) // FIX: input context
			if len(body["properties"].(map[string]any)) > 0 {
				op["requestBody"] = map[string]any{
					"required": body["required"] != nil,
					"content":  jsonContent(body),
				}
			}
		}
	}
	// Path param yang ada di pola tapi tidak dideklarasikan di struct input.
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

	errSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"error": map[string]any{"type": "string"}},
	}
	responses := map[string]any{
		strconv.Itoa(s.cfg.status): map[string]any{
			"description": "Sukses",
			"content":     jsonContent(schemaOf(s.out, 0, false)), // FIX: output context
		},
		"500": map[string]any{"description": "Kesalahan internal", "content": jsonContent(errSchema)},
	}
	if len(params) > 0 || op["requestBody"] != nil {
		responses["400"] = map[string]any{"description": "Input tidak valid", "content": jsonContent(errSchema)}
	}
	op["responses"] = responses
	return op
}

var timeType = reflect.TypeFor[time.Time]()

const maxSchemaDepth = 8

func schemaOf(t reflect.Type, depth int, input bool) map[string]any {
	if depth > maxSchemaDepth {
		return map[string]any{}
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
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
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), depth+1, input)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), depth+1, input)}
	case reflect.Struct:
		return structSchema(t, depth, input, false)
	}
	return map[string]any{}
}

func structSchema(t reflect.Type, depth int, input bool, skipParams bool) map[string]any {
	props := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if skipParams {
			if src, _ := paramSource(f); src != "" {
				continue
			}
		}
		name := jsonName(f)
		if name == "-" {
			continue
		}
		props[name] = schemaOf(f.Type, depth+1, input)

		// FIX: requiredness context-aware
		if input {
			if hasRequired(f) {
				required = append(required, name)
			}
		} else {
			if !hasOmitEmpty(f) {
				required = append(required, name)
			}
		}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
