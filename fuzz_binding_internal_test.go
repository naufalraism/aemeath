package aemeath

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type richText struct{ A, B string }

func (r *richText) UnmarshalText(b []byte) error {
	a, bb, ok := strings.Cut(string(b), ":")
	if !ok {
		return errors.New("want a:b")
	}
	r.A, r.B = a, bb
	return nil
}

func (r richText) MarshalText() ([]byte, error) { return []byte(r.A + ":" + r.B), nil }

type RichEmbedded struct {
	Note string `json:"note" validate:"max=20"`
}

type richIn struct {
	*RichEmbedded
	Raw      json.RawMessage   `json:"raw"`
	Quoted   int64             `json:"quoted,string"`
	Small    uint8             `json:"small"`
	Arr      [3]int            `json:"arr"`
	ByInt    map[int]string    `json:"by_int"`
	Any      any               `json:"any"`
	PtrSlice *[]*int           `json:"ptr_slice"`
	When     time.Time         `json:"when"`
	Text     richText          `json:"text"`
	TextKeys map[richText]int  `json:"text_keys"`
	Num      json.Number       `json:"num"`
	Bytes    []byte            `json:"bytes"`
	Opt      *string           `json:"opt" validate:"min=2"`
	Tags     []string          `json:"tags" validate:"max=3"`
	Kind     int               `json:"kind" validate:"oneof=0 1 2"`
	Nested   map[string]richIn `json:"nested"`
}

// The app must read a JSON body exactly as encoding/json does: 400 exactly
// when encoding/json fails, and, when the request succeeds, the same value.
func FuzzBindMatchesEncodingJSON(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"note":"hi"}`, `{"raw":{"x":[1,2]},"quoted":"12","small":255}`, `{"small":256}`,
		`{"quoted":12}`, `{"arr":[1,2,3,4]}`, `{"by_int":{"1":"a","x":"b"}}`, `{"ptr_slice":[1,null,3]}`,
		`{"when":"2026-01-02T03:04:05Z"}`, `{"when":"yesterday"}`, `{"text":"a:b"}`, `{"text":"ab"}`,
		`{"text_keys":{"a:b":1}}`, `{"num":1e400}`, `{"bytes":"aGk="}`, `{"bytes":"!"}`, `{"opt":""}`,
		`{"tags":["a","b","c","d"]}`, `{"kind":3}`, `{"nested":{"a":{"kind":5}}}`, `{"NOTE":"case"}`,
		`{"note":"a","note":"b"}`, `{"any":{"deep":[{"x":null}]}}`, `{"small":-1}`, `[]`, `null`, ``,
	} {
		f.Add(s)
	}
	app := New()
	registerRuntimeRoute(app, "POST", "/b", reflect.TypeFor[richIn](), reflect.TypeFor[richIn]())
	f.Fuzz(func(t *testing.T, body string) {
		var want richIn
		jsonErr := json.Unmarshal([]byte(body), &want)
		rec := doJSON(app, "POST", "/b", body)
		switch {
		case rec.Code >= 500:
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		case body == "":
			return // no body: nothing to bind
		case jsonErr != nil && rec.Code != 400:
			t.Fatalf("%s: encoding/json fails (%v) but the app answers %d %s", body, jsonErr, rec.Code, rec.Body.String())
		case jsonErr == nil && rec.Code == 400:
			t.Fatalf("%s: encoding/json reads it but the app answers 400 %s", body, rec.Body.String())
		case rec.Code == 200:
			wantJSON, _ := json.Marshal(want)
			if got := strings.TrimSpace(rec.Body.String()); got != string(wantJSON) {
				t.Fatalf("%s:\n app bound %s\n json read %s", body, got, wantJSON)
			}
		}
	})
}

type mpIn struct {
	Name   string                  `form:"name" validate:"max=10"`
	Count  int                     `form:"count"`
	Flags  []string                `form:"flag"`
	Avatar *multipart.FileHeader   `form:"avatar" maxsize:"1KB"`
	Docs   []*multipart.FileHeader `form:"docs"`
}

type mpOut struct {
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Flags  []string `json:"flags"`
	Avatar string   `json:"avatar"`
	Docs   []string `json:"docs"`
}

func readHeader(fh *multipart.FileHeader) string {
	f, err := fh.Open()
	if err != nil {
		return "<open: " + err.Error() + ">"
	}
	defer func() { _ = f.Close() }()
	b, _ := io.ReadAll(f)
	return string(b)
}

// A multipart body, built from fuzzed parts: the app must answer 2xx/4xx,
// and when it accepts the form, report the same fields and file contents an
// independent mime/multipart reader finds.
func FuzzMultipartForm(f *testing.F) {
	f.Add("ana", "3", "x", "avatar.png", []byte("PNG"), "a.txt", []byte("doc"), uint8(0))
	f.Add("", "", "", "", []byte{}, "", []byte{}, uint8(255))
	f.Add("a\x00b", "-1", "y", "../../x", []byte(strings.Repeat("z", 2000)), "b\r\n.txt", []byte("d"), uint8(7))
	f.Add("long name over ten", "1e3", "", "a\"b.png", []byte("\x89PNG"), "c.txt", []byte("e"), uint8(42))
	app := New()
	POST(app, "/m", func(_ *Ctx, in mpIn) (mpOut, error) {
		out := mpOut{Name: in.Name, Count: in.Count, Flags: in.Flags}
		if in.Avatar != nil {
			out.Avatar = readHeader(in.Avatar)
		}
		for _, d := range in.Docs {
			out.Docs = append(out.Docs, readHeader(d))
		}
		return out, nil
	})
	f.Fuzz(func(t *testing.T, name, count, flag, avatarName string, avatar []byte, docName string, doc []byte, layout uint8) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		parts := []func(){
			func() { _ = w.WriteField("name", name) },
			func() { _ = w.WriteField("count", count) },
			func() { _ = w.WriteField("flag", flag) },
			func() { _ = w.WriteField("flag", flag+"2") },
			func() {
				fw, _ := w.CreateFormFile("avatar", avatarName)
				_, _ = fw.Write(avatar)
			},
			func() {
				fw, _ := w.CreateFormFile("docs", docName)
				_, _ = fw.Write(doc)
			},
			func() {
				fw, _ := w.CreateFormFile("docs", docName+"2")
				_, _ = fw.Write(doc)
			},
		}
		for i, p := range parts { // the layout byte drops some parts
			if layout&(1<<uint(i)) == 0 || layout == 0 {
				p()
			}
		}
		_ = w.Close()
		raw := body.Bytes()

		req := httptest.NewRequest("POST", "/m", bytes.NewReader(raw))
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code >= 500 {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		if rec.Code != 200 {
			return
		}
		var got mpOut
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}

		// What an independent reader finds in the same bytes.
		values, files := map[string][]string{}, map[string][]string{}
		mr := multipart.NewReader(bytes.NewReader(raw), w.Boundary())
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				files[p.FormName()] = append(files[p.FormName()], string(b))
			} else {
				values[p.FormName()] = append(values[p.FormName()], string(b))
			}
		}
		first := func(m map[string][]string, k string) string {
			if len(m[k]) == 0 {
				return ""
			}
			return m[k][0]
		}
		if v := first(values, "name"); jsonString(got.Name) != jsonString(v) {
			t.Errorf("name: app %q, reader %q", got.Name, v)
		}
		if v := first(files, "avatar"); jsonString(got.Avatar) != jsonString(v) {
			t.Errorf("avatar: app %d bytes, reader %d bytes", len(got.Avatar), len(v))
		}
		gotDocs, wantDocs := append([]string(nil), got.Docs...), append([]string(nil), files["docs"]...)
		sort.Strings(gotDocs)
		sort.Strings(wantDocs)
		if jsonString(gotDocs) != jsonString(wantDocs) {
			t.Errorf("docs: app %q, reader %q", gotDocs, wantDocs)
		}
		if jsonString(got.Flags) != jsonString(values["flag"]) && (len(got.Flags) != 0 || len(values["flag"]) != 0) {
			t.Errorf("flags: app %q, reader %q", got.Flags, values["flag"])
		}
	})
}

// jsonString compares values the way they travel: through JSON.
func jsonString(v any) string {
	b, _ := json.Marshal(v)
	var back any
	_ = json.Unmarshal(b, &back) // "\ufffd" escapes and raw U+FFFD become the same
	b, _ = json.Marshal(back)
	return string(b)
}

// Any bytes sent as multipart/form-data: 2xx/4xx, never panic or 500.
func FuzzMultipartRawBody(f *testing.F) {
	f.Add([]byte("--B\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nana\r\n--B--\r\n"))
	f.Add([]byte("--B\r\nContent-Disposition: form-data; name=\"avatar\"; filename=\"a.png\"\r\nContent-Type: image/png\r\n\r\nPNG\r\n--B--\r\n"))
	f.Add([]byte("--B\r\nContent-Disposition: form-data; name=\"docs\"; filename=\"\"\r\n\r\n\r\n--B"))
	f.Add([]byte("--B\r\n\r\n--B--"))
	f.Add([]byte(""))
	app := New()
	POST(app, "/m", func(_ *Ctx, in mpIn) (mpOut, error) {
		out := mpOut{Name: in.Name}
		if in.Avatar != nil {
			out.Avatar = readHeader(in.Avatar)
		}
		return out, nil
	})
	f.Fuzz(func(t *testing.T, raw []byte) {
		req := httptest.NewRequest("POST", "/m", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=B")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code >= 500 {
			t.Fatalf("%q: status %d %s", raw, rec.Code, rec.Body.String())
		}
	})
}
