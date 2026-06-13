package jsonschema_test

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestEqual(t *testing.T) {
	for _, tt := range []struct {
		Name   string
		A, B   string
		Expect bool
	}{
		{"both empty", ``, ``, true},

		{"both empty strings", `""`, `""`, true},
		{"strings equal but diff representation", `"A"`, `"\u0041"`, true},

		{"both true", `true`, `true`, true},
		{"both false", `false`, `false`, true},
		{"true vs false not equal", `true`, `false`, false},
		{"false vs false equal", `false`, `false`, true},

		{"both integer zeros", `0`, `0`, true},
		{"both number zeros", `0.0`, `0.0`, true},
		{"different integers", `12`, `13`, false},
		{"same number different significance", `1.0`, `1.00`, true},

		{"numbers are not rounded around 53 bits", `0.1`, `0.1000000000000000055511151231257827021181583404541015625`, false},
		{"integers are not rounded", `9007199254740993`, `9007199254740992`, false},
		{"integers are not rounded around 54 bits", `18014398509481984`, `18014398509481985`, false},
		{"different signed zero", `-0`, `0`, true},

		{"number with capital E exponent", `100`, `1E2`, true},
		{"number with lower e exponent", `100`, `1e2`, true},
		{"number with capital signed exponent", `100`, `1E+2`, true},
		{"number with lower signed exponent", `100`, `1e+2`, true},
		{"number with capital exponent", `100`, `1E2`, true},
		{"number with decimal and capital exponent", `100`, `1.0e2`, true},
		{"number with decimal and lower exponent", `100`, `1.0e2`, true},
		{"number 0 lower exponent", `100`, `100e0`, true},
		{"number 0 capital exponent", `100`, `100E0`, true},
		{"number 0 lower exponent negative zero", `100`, `100e-0`, true},
		{"number 0 capital exponent negative zero", `100`, `100E-0`, true},

		{"both empty arrays", `[]`, `[]`, true},
		{"both array with empty strings", `[""]`, `[""]`, true},
		{"same null element", `[null]`, `[null]`, true},
		{"diff number elements", `[1]`, `[2]`, false},
		{"diff string elements", `["a"]`, `["b"]`, false},
		{"diff order of elements", `["a", "b"]`, `["b", "a"]`, false},
		{"nested array structure matters", `[[1,2],3]`, `[1,[2,3]]`, false},
		{"flat vs nested same leaves", `[1,2,3]`, `[[1,2,3]]`, false},

		{"object vs array same tokens", `{"a":1}`, `["a",1]`, false},

		{"trailing data rejected", `1 2`, `1 2`, false},
		{"one side has trailing data", `1`, `1 2`, false},

		{"both empty objects", `{}`, `{}`, true},

		{"arrays of different length", `[1,2]`, `[1,2,3]`, false},
		{"objects of different length", `{"a":1}`, `{"a":1,"b":2}`, false},
		{"one side empty one side not", ``, `1`, false},
		{"same object one member", `{"a":1}`, `{"a":1}`, true},
		{"objects different member order", `{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{"objects different values", `{"a":1}`, `{"a":2}`, false},
		{"objects different keys", `{"a":1}`, `{"b":1}`, false},
		{"objects different length", `{"a":1}`, `{"a":1,"b":2}`, false},
		{"nested objects equal", `{"a":{"b":1}}`, `{"a":{"b":1}}`, true},
		{"nested objects differ deep", `{"a":{"b":1}}`, `{"a":{"b":2}}`, false},
		{"nested arrays equal", `[[1,2],[3,4]]`, `[[1,2],[3,4]]`, true},
		{"nested mismatch", `[[1,2],3]`, `[1,[2,3]]`, false},
		{"key with unicode escape", `{"\u00e9":1}`, `{"é":1}`, true},
		{"key with surrogate pair", `{"\uD83D\uDE00":1}`, `{"😀":1}`, true},
		{"object in array", `[{"a":1},{"b":2}]`, `[{"a":1},{"b":2}]`, true},
		{"numbers inside objects", `{"x":0.3}`, `{"x":3e-1}`, true},
		{"trailing data rejected A", `1 2`, `1`, false},
		{"only whitespace", `   `, `   `, false}, // not valid JSON
	} {
		t.Run(tt.Name, func(t *testing.T) {
			equal, err := jsonschema.Equal([]byte(tt.A), []byte(tt.B))
			if err != nil {
				t.Error(err)
			}
			if equal != tt.Expect {
				t.Fail()
			}
		})
	}
}

func TestEqual_errors(t *testing.T) {
	for _, tt := range []struct {
		Name string
		A, B string
	}{
		{"duplicate key in A", `{"a":1,"a":2}`, `{"a":1}`},
		{"duplicate key in B", `{"a":1}`, `{"a":1,"a":2}`},
		{"duplicate key nested", `{"x":{"a":1,"a":2}}`, `{"x":{"a":1}}`},
		{"malformed A object", `{`, `{}`},
		{"malformed B object", `{}`, `{`},
		{"malformed A value", `{"a":}`, `{"a":1}`},
		{"garbage both", `xxx`, `xxx`},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			if _, err := jsonschema.Equal([]byte(tt.A), []byte(tt.B)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func FuzzEqualReflexive(f *testing.F) {
	for _, s := range []string{
		`null`, `true`, `false`, `0`, `1.5`, `"hello"`, `[]`, `{}`,
		`{"a":1}`, `[1,2,3]`, `{"nested":{"x":[1,2]}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !jsontext.Value(data).IsValid() {
			return
		}
		eq, err := jsonschema.Equal(data, data)
		if err != nil {
			return
		}
		if !eq {
			t.Errorf("Equal(x,x)=false for %q", data)
		}
	})
}

func FuzzEqualSymmetric(f *testing.F) {
	seeds := [][2]string{
		{`1`, `1.0`},
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`},
		{`[1,2]`, `[1,2,3]`},
		{`null`, `0`},
		{`"a"`, `"b"`},
	}
	for _, s := range seeds {
		f.Add([]byte(s[0]), []byte(s[1]))
	}
	f.Fuzz(func(t *testing.T, a, b []byte) {
		ab, errA := jsonschema.Equal(a, b)
		ba, errB := jsonschema.Equal(b, a)
		if (errA == nil) != (errB == nil) {
			t.Errorf("asymmetric error: Equal(a,b)=%v, Equal(b,a)=%v", errA, errB)
			return
		}
		if errA == nil && ab != ba {
			t.Errorf("asymmetric result: Equal(%q,%q)=%v, Equal(%q,%q)=%v", a, b, ab, b, a, ba)
		}
	})
}
