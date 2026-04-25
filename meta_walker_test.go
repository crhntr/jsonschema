package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/go-json-experiment/json"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/jsonptr"
)

const walkerSampleSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.com/walker",
  "type": "object",
  "title": "Outer",
  "$defs": {
    "id": {
      "type": "string",
      "title": "Identifier"
    }
  },
  "properties": {
    "name": { "type": "string" },
    "child": {
      "type": "object",
      "properties": {
        "ref": { "$ref": "#/$defs/id" }
      }
    }
  },
  "allOf": [
    { "type": "object" },
    { "type": "object", "title": "second" }
  ]
}`

func TestMetaImplementsWalker(t *testing.T) {
	doc, err := jsonschema.Parse([]byte(walkerSampleSchema))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		ptr     jsonptr.Pointer
		liveIs  func(any) bool
		raw     string
	}{
		{
			ptr:    "/$defs/id",
			liveIs: func(v any) bool { _, ok := v.(*jsonschema.Meta); return ok },
			raw:    `"type":"string"`,
		},
		{
			ptr:    "/properties/child/properties/ref",
			liveIs: func(v any) bool { _, ok := v.(*jsonschema.Meta); return ok },
			raw:    `"$ref":"#/$defs/id"`,
		},
		{
			ptr:    "/allOf/1",
			liveIs: func(v any) bool { _, ok := v.(*jsonschema.Meta); return ok },
			raw:    `"title":"second"`,
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.ptr), func(t *testing.T) {
			raw, live, err := jsonptr.FindValue(tc.ptr, doc)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.liveIs(live) {
				t.Errorf("live = %T, expected *jsonschema.Meta", live)
			}
			if !strings.Contains(string(raw), tc.raw) {
				t.Errorf("raw = %s, want substring %s", raw, tc.raw)
			}
		})
	}
}

// TestMetaWalkerHandsOffToReflection verifies that when a pointer descends
// past a Meta-traversable subschema into a scalar field (e.g., title),
// Meta hands off the leftover tokens. jsonptr.FindValue then falls back
// to byte-mode (via Meta's MarshalJSONTo) and resolves the rest.
func TestMetaWalkerHandsOffToReflection(t *testing.T) {
	doc, err := jsonschema.Parse([]byte(walkerSampleSchema))
	if err != nil {
		t.Fatal(err)
	}
	raw, live, err := jsonptr.FindValue("/$defs/id/title", doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"Identifier"` {
		t.Errorf("raw = %s, want \"Identifier\"", raw)
	}
	if live != "Identifier" {
		t.Errorf("live = %v, want Identifier", live)
	}
}

func TestMetaWalkerForwardsOptions(t *testing.T) {
	doc, err := jsonschema.Parse([]byte(walkerSampleSchema))
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic should propagate into the marshal call at the leaf.
	// Verify the option doesn't break the descent and the bytes contain
	// the expected fields.
	raw, _, err := jsonptr.FindValue("/$defs/id", doc, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, `"title":"Identifier"`) {
		t.Errorf("raw missing title: %s", got)
	}
	if !strings.Contains(got, `"type":"string"`) {
		t.Errorf("raw missing type: %s", got)
	}
}
