package aemeath

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type mkText struct{ a, b int }

func (k mkText) MarshalText() ([]byte, error) {
	return []byte(strings.Repeat("x", k.a) + "-" + strings.Repeat("y", k.b)), nil
}

func (k *mkText) UnmarshalText(b []byte) error {
	l, r, _ := strings.Cut(string(b), "-")
	k.a, k.b = len(l), len(r)
	return nil
}

type mkStringer string

func (s mkStringer) String() string { return "stringer:" + string(s) }

type mkVal struct {
	Email string `json:"email" validate:"email"`
}

// The names in error paths, and the lookup of which keys were sent, must use
// the key exactly as encoding/json wrote it.
func TestMapKeyNamesMatchEncodingJSON(t *testing.T) {
	for _, m := range []any{
		map[string]int{"b": 1, "a": 2},
		map[int]int{-3: 1, 10: 2},
		map[uint8]int{7: 1},
		map[mkText]int{{1, 2}: 1},
		map[mkStringer]int{"s": 1}, // a String method does not change the JSON key
	} {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]any
		if err := json.Unmarshal(b, &keys); err != nil {
			t.Fatal(err)
		}
		it := reflect.ValueOf(m).MapRange()
		for it.Next() {
			if name := mapKeyName(it.Key()); keys[name] == nil {
				t.Errorf("%T: key %v named %q, JSON has %s", m, it.Key(), name, b)
			}
		}
	}
}

func TestMapErrorsAreSortedByKey(t *testing.T) {
	type in struct {
		M map[string]mkVal `json:"m"`
	}
	var v in
	body := []byte(`{"m":{"c":{"email":"x"},"a":{"email":"x"},"b":{"email":"x"}}}`)
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	var errs []FieldError
	planFor(reflect.TypeFor[in]()).validate(reflect.ValueOf(v), "", decodeSent(body), &errs, nil)
	var got []string
	for _, e := range errs {
		got = append(got, e.Field)
	}
	if want := "m[a].email m[b].email m[c].email"; strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}
