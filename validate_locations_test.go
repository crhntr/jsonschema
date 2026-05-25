package jsonschema_test

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

// findLeaf walks the failure tree under root and returns the first
// invalid Output whose KeywordLocation contains kwSubstr. Returns nil
// if none match. Used to assert that a failure surfaces at the
// expected schema location.
func findLeaf(root jsonschema.Output, kwSubstr string) *jsonschema.Output {
	if !root.Valid && strings.Contains(root.KeywordLocation, kwSubstr) && len(root.Errors) == 0 {
		out := root
		return &out
	}
	for _, c := range root.Errors {
		if got := findLeaf(c, kwSubstr); got != nil {
			return got
		}
	}
	return nil
}

func TestValidateKeywordLocationProperty(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/v/prop", []byte(`{
		"$id": "https://example.com/v/prop",
		"properties": {
			"name": { "type": "integer" }
		}
	}`))
	out := doc.Validate("inst", []byte(`{"name":"oops"}`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	leaf := findLeaf(out, "/properties/name/type")
	if leaf == nil {
		t.Fatalf("no leaf with /properties/name/type; got %+v", out)
	}
	if leaf.InstanceLocation != "/name" {
		t.Errorf("InstanceLocation = %q, want /name", leaf.InstanceLocation)
	}
}

func TestValidateKeywordLocationItem(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/v/item", []byte(`{
		"$id": "https://example.com/v/item",
		"items": { "type": "string" }
	}`))
	out := doc.Validate("inst", []byte(`["ok", 42, "ok2"]`))
	if out.Valid {
		// expected
	}
	leaf := findLeaf(out, "/items/type")
	if leaf == nil {
		t.Fatalf("no leaf at /items/type; got %+v", out)
	}
	if leaf.InstanceLocation != "/1" {
		t.Errorf("InstanceLocation = %q, want /1", leaf.InstanceLocation)
	}
}

func TestValidateKeywordLocationAllOf(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/v/allof", []byte(`{
		"$id": "https://example.com/v/allof",
		"allOf": [
			{ "type": "string" },
			{ "minLength": 5 }
		]
	}`))
	out := doc.Validate("inst", []byte(`"hi"`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	leaf := findLeaf(out, "/allOf/1/minLength")
	if leaf == nil {
		t.Fatalf("no leaf at /allOf/1/minLength; got %+v", out)
	}
}

func TestValidateAbsoluteKeywordLocationOnRef(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/v/ref", []byte(`{
		"$id": "https://example.com/v/ref",
		"$defs": {
			"positive": { "type": "integer", "minimum": 1 }
		},
		"properties": {
			"n": { "$ref": "#/$defs/positive" }
		}
	}`))
	out := doc.Validate("inst", []byte(`{"n": 0}`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	// Find any leaf whose KeywordLocation contains $ref. By the spec
	// rule, AbsoluteKeywordLocation must be populated on those.
	var found bool
	var walk func(o jsonschema.Output)
	walk = func(o jsonschema.Output) {
		if strings.Contains(o.KeywordLocation, "$ref") && o.AbsoluteKeywordLocation == "" {
			t.Errorf("KeywordLocation %q crosses $ref but AbsoluteKeywordLocation is empty", o.KeywordLocation)
		}
		if strings.Contains(o.KeywordLocation, "$ref") && strings.Contains(o.AbsoluteKeywordLocation, "#/$defs/positive") {
			found = true
		}
		for _, c := range o.Errors {
			walk(c)
		}
	}
	walk(out)
	if !found {
		t.Errorf("expected a unit with AbsoluteKeywordLocation rooted at #/$defs/positive; got tree %+v", out)
	}
}

func TestValidateInstanceLocationNested(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/v/nested", []byte(`{
		"$id": "https://example.com/v/nested",
		"properties": {
			"a": {
				"properties": {
					"b": { "type": "integer" }
				}
			}
		}
	}`))
	out := doc.Validate("inst", []byte(`{"a":{"b":"oops"}}`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	leaf := findLeaf(out, "/properties/a/properties/b/type")
	if leaf == nil {
		t.Fatalf("no leaf at deep keyword; got %+v", out)
	}
	if leaf.InstanceLocation != "/a/b" {
		t.Errorf("InstanceLocation = %q, want /a/b", leaf.InstanceLocation)
	}
}

func TestValidateOutputMarshalsCleanly(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/v/m", []byte(`{
		"$id": "https://example.com/v/m",
		"type": "integer"
	}`))
	out := doc.Validate("inst", []byte(`"x"`))
	if out.Valid {
		// expected
	}
	// MarshalJSON must succeed — the Output must satisfy spec invariants.
	got, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal: %v\nout: %+v", err, out)
	}
	if !strings.Contains(string(got), `"/type"`) {
		t.Errorf("marshaled JSON missing /type keywordLocation: %s", got)
	}
}
