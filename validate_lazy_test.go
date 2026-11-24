package aemeath

import (
	"encoding/json"
	"reflect"
	"testing"
)

type lazyChild struct {
	Email string `json:"email" validate:"email"`
	Qty   int    `json:"qty" validate:"gte=1"`
}

// LazyEmbedded is exported: encoding/json cannot fill a nil pointer to an unexported embedded struct.
type LazyEmbedded struct {
	Note string `json:"note" validate:"min=2"`
}

type lazyIn struct {
	*LazyEmbedded
	Name     string               `json:"name" validate:"required"`
	Website  string               `json:"website" validate:"url"`
	Tags     []string             `json:"tags" validate:"min=1"`
	Kind     int                  `json:"kind" validate:"oneof=1 2"`
	Ratio    float64              `json:"ratio" validate:"gt=0"`
	Age      *int                 `json:"age" validate:"gte=18"`
	Ok       bool                 `json:"ok"`
	Child    lazyChild            `json:"child"`
	PChild   *lazyChild           `json:"pchild"`
	Children []lazyChild          `json:"children"`
	ByKey    map[string]lazyChild `json:"by_key"`
	Matrix   [][]lazyChild        `json:"matrix"`
}

// validateBoth runs the validation the way bind does (first without the keys,
// decoding the body only if asked) and the way it used to (always decoding),
// and reports both results.
func validateBoth(t testing.TB, body []byte) (lazy, eager []FieldError, decoded bool, ok bool) {
	var in lazyIn
	if json.Unmarshal(body, &in) != nil {
		return nil, nil, false, false
	}
	plan := planFor(reflect.TypeFor[lazyIn]())
	v := reflect.ValueOf(&in).Elem()

	plan.validate(v, "", decodeSent(body), &eager, 0, nil)

	var need bool
	plan.validate(v, "", nil, &lazy, 0, &need)
	if need {
		lazy = nil
		plan.validate(v, "", decodeSent(body), &lazy, 0, nil)
	}
	return lazy, eager, need, true
}

var lazyCases = []struct {
	body       string
	wantDecode bool // whether the body has to be decoded a second time
}{
	{`{"name":"Ana","website":"https://a.b","tags":["x"],"kind":1,"ratio":1,"age":20,"ok":true,"child":{"email":"a@b.co","qty":1}}`, false},
	{`{"name":"Ana"}`, true}, // child.email is empty: was it sent? (no)
	{`{"name":"Ana","website":""}`, true},
	{`{"name":"Ana","tags":[]}`, true},
	{`{"name":"Ana","kind":0}`, true},
	{`{"name":"Ana","ratio":0}`, true},
	{`{"name":"Ana","age":null}`, true},
	{`{"name":"Ana","age":10}`, true},
	{`{"name":""}`, true},
	{`{}`, true},
	{`{"name":"Ana","child":{"email":"","qty":0}}`, true},
	{`{"name":"Ana","pchild":{"email":"","qty":0}}`, true},
	{`{"name":"Ana","pchild":null}`, true},
	{`{"name":"Ana","children":[{"email":"a@b.co","qty":1},{"qty":0},{}]}`, true},
	{`{"name":"Ana","by_key":{"x":{"email":"bad"},"y":{}}}`, true},
	{`{"name":"Ana","matrix":[[{"qty":0}],[]]}`, true},
	{`{"name":"Ana","note":""}`, true},
	{`{"name":"Ana","note":"x"}`, true},
	{`{"name":"Ana","website":"javascript:1","tags":null,"kind":3}`, true},
}

func TestLazyKeysGiveTheSameErrors(t *testing.T) {
	for _, tc := range lazyCases {
		lazy, eager, decoded, ok := validateBoth(t, []byte(tc.body))
		if !ok {
			t.Fatalf("%s does not decode", tc.body)
		}
		if !reflect.DeepEqual(lazy, eager) {
			t.Errorf("%s\n lazy:  %v\n eager: %v", tc.body, lazy, eager)
		}
		if tc.body == lazyCases[0].body && decoded {
			t.Errorf("a body with every validated value filled in was decoded a second time")
		}
	}
}

func FuzzLazyKeysGiveTheSameErrors(f *testing.F) {
	for _, tc := range lazyCases {
		f.Add([]byte(tc.body))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		lazy, eager, _, ok := validateBoth(t, body)
		if ok && !reflect.DeepEqual(lazy, eager) {
			t.Errorf("%s\n lazy:  %v\n eager: %v", body, lazy, eager)
		}
	})
}
