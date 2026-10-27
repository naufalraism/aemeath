package aemeath

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

// maxClientResponseBytes caps how much of a response the Go client will read.
const maxClientResponseBytes = 32 << 20

// Endpoint is a typed description of an API route. Share one Endpoint value
// between the server (Register) and the Go client (Call) to keep them in sync.
type Endpoint[In, Out any] struct {
	Method, Path string
}

// NewEndpoint creates an Endpoint.
func NewEndpoint[In, Out any](method, path string) Endpoint[In, Out] {
	return Endpoint[In, Out]{Method: method, Path: path}
}

// defaultHTTPClient has a timeout so the client never hangs forever
// if WithHTTPClient was not configured.
var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

// Client calls Aemeath endpoints from Go.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Header  http.Header
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient replaces the underlying *http.Client.
func WithHTTPClient(h *http.Client) ClientOption { return func(c *Client) { c.HTTP = h } }

// WithHeader sets a header sent with every request.
func WithHeader(k, v string) ClientOption { return func(c *Client) { c.Header.Set(k, v) } }

// NewClient creates a Client for the given base URL.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{BaseURL: baseURL, Header: http.Header{}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Call sends the request described by the endpoint and decodes the response.
// A non-2xx response is returned as *HTTPError (with Errors set for validation failures).
func (e Endpoint[In, Out]) Call(ctx context.Context, c *Client, in In) (Out, error) {
	var out Out
	enc, err := encodeRequest(e.Method, e.Path, in)
	if err != nil {
		return out, err
	}
	target := strings.TrimRight(c.BaseURL, "/") + enc.path
	if len(enc.query) > 0 {
		target += "?" + enc.query.Encode()
	}
	var rd io.Reader
	if enc.body != nil {
		rd = bytes.NewReader(enc.body)
	}
	req, err := http.NewRequestWithContext(ctx, e.Method, target, rd)
	if err != nil {
		return out, err
	}
	for k, vs := range c.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for k, vs := range enc.header {
		req.Header[k] = vs
	}
	for _, ck := range enc.cookies {
		req.AddCookie(ck)
	}
	if enc.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTPClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return out, err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxClientResponseBytes+1))
	if err != nil {
		return out, err
	}
	if len(data) > maxClientResponseBytes {
		return out, fmt.Errorf("aemeath: response exceeds %d bytes", maxClientResponseBytes)
	}
	if res.StatusCode >= 400 {
		he := &HTTPError{}
		_ = json.Unmarshal(data, he)
		he.Code = res.StatusCode
		if he.Message == "" {
			he.Message = http.StatusText(res.StatusCode)
		}
		return out, he
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return out, fmt.Errorf("aemeath: failed to decode response: %w", err)
		}
	}
	return out, nil
}

type encodedRequest struct {
	path    string
	query   url.Values
	header  http.Header
	cookies []*http.Cookie
	body    []byte
}

// formatParam renders a path/query/header/cookie value. Types implementing
// encoding.TextMarshaler (time.Time, UUID types, ...) use it.
func formatParam(v reflect.Value) string {
	if v.CanInterface() {
		if tm, ok := v.Interface().(encoding.TextMarshaler); ok {
			if b, err := tm.MarshalText(); err == nil {
				return string(b)
			}
		}
		p := reflect.New(v.Type())
		p.Elem().Set(v)
		if tm, ok := p.Interface().(encoding.TextMarshaler); ok {
			if b, err := tm.MarshalText(); err == nil {
				return string(b)
			}
		}
		return fmt.Sprint(v.Interface())
	}
	return fmt.Sprint(v)
}

// encodeRequest splits the input into path, query, headers, cookies and body.
//
// Zero values of non-pointer parameters are treated as "not set" and omitted.
// Use a pointer (e.g. *bool, *int) to send an explicit zero value.
func encodeRequest(method, pattern string, in any) (encodedRequest, error) {
	enc := encodedRequest{path: openAPIPath(pattern), query: url.Values{}, header: http.Header{}}
	v := reflect.ValueOf(in)
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			v = reflect.Value{}
			break
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return enc, nil
	}
	if v.Kind() != reflect.Struct {
		if bodyMethod(method) {
			b, err := json.Marshal(v.Interface())
			enc.body = b
			return enc, err
		}
		return enc, nil
	}

	var paramNames []string
	for _, ff := range flatFields(v.Type()) {
		src, name := paramSource(ff.StructField)
		if src == "" {
			continue
		}
		paramNames = append(paramNames, jsonName(ff.StructField))
		fv, ok := fieldByPath(v, ff.Path, false)
		emptyPath := func() error { return fmt.Errorf("aemeath: path param %q is empty", name) }
		if !ok {
			if src == "path" {
				return enc, emptyPath()
			}
			continue
		}

		// Dereference pointers so the value is sent, not a memory address.
		// A nil pointer means "not set"; a non-nil pointer to a zero value is
		// an explicit value and is sent.
		raw := fv
		isPtr := fv.Kind() == reflect.Pointer
		nilPtr := false
		for raw.Kind() == reflect.Pointer {
			if raw.IsNil() {
				nilPtr = true
				break
			}
			raw = raw.Elem()
		}

		// Repeated query keys for slices: ?tag=a&tag=b
		if src == "query" && !nilPtr && raw.Kind() == reflect.Slice && raw.Type().Elem().Kind() != reflect.Uint8 && !isTextType(raw.Type()) {
			for j := 0; j < raw.Len(); j++ {
				enc.query.Add(name, formatParam(raw.Index(j)))
			}
			continue
		}

		if nilPtr || (!isPtr && raw.IsZero()) {
			if src == "path" {
				return enc, emptyPath()
			}
			continue
		}
		s := formatParam(raw)
		switch src {
		case "path":
			if s == "" {
				return enc, emptyPath()
			}
			enc.path = strings.ReplaceAll(enc.path, "{"+name+"}", url.PathEscape(s))
		case "query":
			enc.query.Set(name, s)
		case "header":
			enc.header.Set(name, s)
		case "cookie":
			enc.cookies = append(enc.cookies, &http.Cookie{Name: name, Value: s})
		}
	}

	if bodyMethod(method) {
		b, err := json.Marshal(v.Interface())
		if err != nil {
			return enc, err
		}
		if len(paramNames) > 0 {
			var m map[string]json.RawMessage
			if json.Unmarshal(b, &m) == nil {
				for _, n := range paramNames {
					delete(m, n)
				}
				if b, err = json.Marshal(m); err != nil {
					return enc, err
				}
			}
		}
		enc.body = b
	}
	return enc, nil
}
