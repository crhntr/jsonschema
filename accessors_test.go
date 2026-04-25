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
