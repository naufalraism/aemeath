package aemeath

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

type Endpoint[In, Out any] struct {
	Method, Path string
}

func NewEndpoint[In, Out any](method, path string) Endpoint[In, Out] {
	return Endpoint[In, Out]{Method: method, Path: path}
}

// defaultHTTPClient punya timeout agar client tidak hang selamanya
// jika lupa mengonfigurasi lewat WithHTTPClient.
var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Header  http.Header
}

type ClientOption func(*Client)

func WithHTTPClient(h *http.Client) ClientOption { return func(c *Client) { c.HTTP = h } }
func WithHeader(k, v string) ClientOption        { return func(c *Client) { c.Header.Set(k, v) } }

func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{BaseURL: baseURL, Header: http.Header{}}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (e Endpoint[In, Out]) Call(ctx context.Context, c *Client, in In) (Out, error) {
	var out Out
	path, query, body, err := encodeRequest(e.Method, e.Path, in)
	if err != nil {
		return out, err
	}
	target := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
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
	if body != nil {
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
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return out, err
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
			return out, fmt.Errorf("aemeath: respons tidak bisa di-decode: %w", err)
		}
	}
	return out, nil
}

func encodeRequest(method, pattern string, in any) (string, url.Values, []byte, error) {
	path := openAPIPath(pattern)
	query := url.Values{}
	v := reflect.ValueOf(in)
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			v = reflect.Value{}
			break
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return path, query, nil, nil
	}
	if v.Kind() != reflect.Struct {
		if bodyMethod(method) {
			b, err := json.Marshal(v.Interface())
			return path, query, b, err
		}
		return path, query, nil, nil
	}

	t := v.Type()
	var paramNames []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		src, name := paramSource(f)
		if src == "" {
			continue
		}
		paramNames = append(paramNames, jsonName(f))
		fv := v.Field(i)
		// FIX: dereference pointer agar nilai scalar yang dikirim,
		// bukan alamat memori. Nil pointer dianggap kosong.
		raw := fv
		isNilPtr := false
		for raw.Kind() == reflect.Pointer {
			if raw.IsNil() {
				isNilPtr = true
				break
			}
			raw = raw.Elem()
		}
		if isNilPtr || raw.IsZero() {
			if src == "path" {
				return "", nil, nil, fmt.Errorf("aemeath: path param %q kosong", name)
			}
			continue
		}
		s := fmt.Sprint(raw.Interface())
		if src == "path" {
			path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(s))
		} else {
			query.Set(name, s)
		}
	}

	var body []byte
	if bodyMethod(method) {
		b, err := json.Marshal(v.Interface())
		if err != nil {
			return "", nil, nil, err
		}
		if len(paramNames) > 0 {
			var m map[string]json.RawMessage
			if json.Unmarshal(b, &m) == nil {
				for _, n := range paramNames {
					delete(m, n)
				}
				if b, err = json.Marshal(m); err != nil {
					return "", nil, nil, err
				}
			}
		}
		body = b
	}
	return path, query, body, nil
}
