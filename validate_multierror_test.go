package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

// findKeyword walks o (preorder) and returns the first Output whose
// KeywordLocation equals exactly kw, or nil if none.
func findKeyword(o jsonschema.Output, kw string) *jsonschema.Output {
	if o.KeywordLocation == kw {
		out := o
		return &out
	}
	for _, c := range o.Errors {
		if got := findKeyword(c, kw); got != nil {
			return got
		}
	}
	return nil
}

func TestMultiErrorAllOf(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/me/allof", []byte(`{
		"$id": "https://example.com/me/allof",
		"allOf": [
			{ "type": "integer" },
			{ "const": "hello" }
		]
	}`))
	// "hi" is neither an integer nor equal to "hello", so both branches fail.
	out := doc.Validate("inst", []byte(`"hi"`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	allOf := findKeyword(out, "/allOf")
	if allOf == nil {
		t.Fatalf("missing /allOf unit; tree: %+v", out)
	}
	if len(allOf.Errors) != 2 {
		t.Errorf("/allOf.Errors len = %d, want 2 (one per failing branch); got: %+v", len(allOf.Errors), allOf.Errors)
	}
}

func TestMultiErrorPropertiesBothBad(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/me/props", []byte(`{
		"$id": "https://example.com/me/props",
		"properties": {
			"a": { "type": "string" },
			"b": { "type": "integer" }
		}
	}`))
	out := doc.Validate("inst", []byte(`{"a": 1, "b": "x"}`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	props := findKeyword(out, "/properties")
	if props == nil {
		t.Fatalf("missing /properties unit; tree: %+v", out)
	}
	if len(props.Errors) != 2 {
		t.Errorf("/properties.Errors len = %d, want 2", len(props.Errors))
	}
}

func TestMultiErrorUniqueItemsAllPairs(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/me/unique", []byte(`{
		"$id": "https://example.com/me/unique",
		"uniqueItems": true
	}`))
	out := doc.Validate("inst", []byte(`[1, 1, 2, 2]`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	uniq := findKeyword(out, "/uniqueItems")
	if uniq == nil {
		t.Fatalf("missing /uniqueItems unit; tree: %+v", out)
	}
	// Should report both duplicate pairs (0,1) and (2,3).
	for _, want := range []string{"items 0 and 1 are equal", "items 2 and 3 are equal"} {
		if !strings.Contains(uniq.Error, want) {
			t.Errorf("/uniqueItems.Error missing %q; got %q", want, uniq.Error)
		}
	}
}

func TestMultiErrorAnyOfAllBranches(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/me/anyof", []byte(`{
		"$id": "https://example.com/me/anyof",
		"anyOf": [
			{ "type": "string" },
			{ "type": "integer" }
		]
	}`))
	out := doc.Validate("inst", []byte(`true`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	any := findKeyword(out, "/anyOf")
	if any == nil {
		t.Fatalf("missing /anyOf unit; tree: %+v", out)
	}
	if len(any.Errors) != 2 {
		t.Errorf("/anyOf.Errors len = %d, want 2 branches reported; got: %+v", len(any.Errors), any.Errors)
	}
}

func TestMultiErrorOneOfTwoMatches(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/me/oneof", []byte(`{
		"$id": "https://example.com/me/oneof",
		"oneOf": [
			{ "minimum": 0 },
			{ "maximum": 10 }
		]
	}`))
	out := doc.Validate("inst", []byte(`5`))
	if out.Valid {
		t.Fatal("expected invalid (5 satisfies both branches)")
	}
	one := findKeyword(out, "/oneOf")
	if one == nil {
		t.Fatalf("missing /oneOf unit; tree: %+v", out)
	}
	if !strings.Contains(one.Error, "2 subschemas matched") {
		t.Errorf("/oneOf.Error should report 2 matches; got %q", one.Error)
	}
	// Both branches were valid; oneOf should report both as branch units.
	if len(one.Errors) != 2 {
		t.Errorf("/oneOf.Errors len = %d, want 2 branches reported", len(one.Errors))
	}
}

func TestMultiErrorItemsManyBad(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/me/items", []byte(`{
		"$id": "https://example.com/me/items",
		"items": { "type": "string" }
	}`))
	out := doc.Validate("inst", []byte(`["ok", 1, 2, 3]`))
	if out.Valid {
		t.Fatal("expected invalid")
	}
	items := findKeyword(out, "/items")
	if items == nil {
		t.Fatalf("missing /items unit; tree: %+v", out)
	}
	// Three failing items at /1, /2, /3.
	if len(items.Errors) != 3 {
		t.Errorf("/items.Errors len = %d, want 3", len(items.Errors))
	}
	wantInst := map[string]bool{"/1": true, "/2": true, "/3": true}
	for _, e := range items.Errors {
		if !wantInst[e.InstanceLocation] {
			t.Errorf("unexpected InstanceLocation %q", e.InstanceLocation)
		}
		delete(wantInst, e.InstanceLocation)
	}
	if len(wantInst) > 0 {
		t.Errorf("missing InstanceLocations: %v", wantInst)
	}
}
