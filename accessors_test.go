package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestSchemaAnchorLookup(t *testing.T) {
	body := []byte(`{
		"$id": "https://example.com/anchors",
		"$defs": {
			"a": { "$anchor": "alpha", "type": "string" }
		}
	}`)
	var r jsonschema.Resolver
	if _, err := r.Load("https://example.com/anchors", body); err != nil {
		t.Fatal(err)
	}
	schema, err := r.Resolve(t.Context(), "https://example.com/anchors")
	if err != nil {
		t.Fatal(err)
	}
	got := schema.Anchor("alpha")
	if got == nil {
		t.Fatal("Anchor(\"alpha\") returned nil")
	}
	obj, ok := got.TypeObject()
	if !ok {
		t.Fatal("anchor target is not an object schema")
	}
	if s, _ := obj.Type.TypeString(); string(s) != "string" {
		t.Errorf("anchor target type = %q, want string", s)
	}
	if missing := schema.Anchor("nope"); missing != nil {
		t.Errorf("Anchor(\"nope\") = %v, want nil", missing)
	}
}

func TestTypeValidate(t *testing.T) {
	cases := []struct {
		name  string
		input string
		ok    bool
	}{
		{"valid string", `"string"`, true},
		{"invalid string", `"flubber"`, false},
		{"valid array", `["string","integer"]`, true},
		{"empty array", `[]`, false},
		{"invalid array entry", `["string","squid"]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := jsonschema.Parse([]byte(`{"type":` + tc.input + `}`))
			if err != nil {
				t.Fatal(err)
			}
			obj, _ := s.TypeObject()
			err = obj.Type.Validate()
			gotOK := err == nil
			if gotOK != tc.ok {
				t.Errorf("Type.Validate() err=%v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestSimpleTypeValidate(t *testing.T) {
	for _, name := range []string{"string", "integer", "number", "object", "array", "boolean", "null"} {
		t.Run(name, func(t *testing.T) {
			if err := jsonschema.SimpleType(name).Validate(); err != nil {
				t.Errorf("SimpleType(%q).Validate() = %v, want nil", name, err)
			}
		})
	}
	err := jsonschema.SimpleType("flubber").Validate()
	if err == nil {
		t.Fatal("expected error for unknown SimpleType")
	}
	if !strings.Contains(err.Error(), "invalid SimpleType") {
		t.Errorf("error = %v, want substring 'invalid SimpleType'", err)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := jsonschema.Parse([]byte(`{not json`)); err == nil {
		t.Error("expected error from Parse on invalid JSON")
	}
}

func TestSubschemasYieldsEachChildExactlyOnce(t *testing.T) {
	body := []byte(`{
		"$defs": {
			"a": {"description": "defs-a"},
			"b": {"description": "defs-b"}
		},
		"properties": {
			"p1": {"description": "props-p1"},
			"p2": {"description": "props-p2"}
		},
		"patternProperties": {
			"^x": {"description": "patternProps-x"}
		},
		"dependentSchemas": {
			"k": {"description": "depSchema-k"}
		},
		"allOf": [
			{"description": "allOf-0"},
			{"description": "allOf-1"}
		],
		"anyOf": [{"description": "anyOf-0"}],
		"oneOf": [{"description": "oneOf-0"}],
		"prefixItems": [{"description": "prefixItems-0"}],
		"if":   {"description": "if"},
		"then": {"description": "then"},
		"else": {"description": "else"},
		"not":  {"description": "not"},
		"items":                  {"description": "items"},
		"contains":               {"description": "contains"},
		"additionalProperties":   {"description": "additionalProperties"},
		"unevaluatedProperties": {"description": "unevaluatedProperties"},
		"unevaluatedItems":      {"description": "unevaluatedItems"},
		"propertyNames":         {"description": "propertyNames"}
	}`)
	schema, err := jsonschema.Parse(body)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"defs-a", "defs-b",
		"props-p1", "props-p2",
		"patternProps-x",
		"depSchema-k",
		"allOf-0", "allOf-1",
		"anyOf-0",
		"oneOf-0",
		"prefixItems-0",
		"if", "then", "else", "not",
		"items", "contains",
		"additionalProperties", "unevaluatedProperties", "unevaluatedItems",
		"propertyNames",
	}

	counts := map[string]int{}
	for sub := range schema.Subschemas() {
		obj, ok := sub.TypeObject()
		if !ok {
			t.Errorf("yielded a non-object subschema: %#v", sub)
			continue
		}
		if obj.Description == "" {
			t.Errorf("yielded subschema has no description: %#v", sub)
			continue
		}
		counts[obj.Description]++
	}

	for _, d := range want {
		if got := counts[d]; got != 1 {
			t.Errorf("description %q yielded %d times, want 1", d, got)
		}
		delete(counts, d)
	}
	for d, n := range counts {
		t.Errorf("unexpected description %q yielded %d time(s)", d, n)
	}
}
