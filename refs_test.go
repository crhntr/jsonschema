package jsonschema_test

import (
	"testing"

	"github.com/crhntr/jsonschema"
)

const refsSampleSchema = `{
  "$id": "https://example.com/refs-iter",
  "$defs": {
    "name": { "$anchor": "name", "type": "string" },
    "id":   { "$ref": "#name" }
  },
  "type": "object",
  "properties": {
    "n":     { "$ref": "#/$defs/name" },
    "child": {
      "type": "object",
      "properties": {
        "ref": { "$ref": "#/$defs/id" }
      }
    }
  },
  "allOf": [
    { "$ref": "#/$defs/name" },
    { "$dynamicRef": "#meta" }
  ]
}`

func TestMetaRefsYieldsEveryRef(t *testing.T) {
	doc, err := jsonschema.Parse([]byte(refsSampleSchema))
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for sub := range doc.Refs() {
		obj, _ := sub.TypeObject()
		switch {
		case obj.Ref != "":
			refs = append(refs, obj.Ref)
		case obj.DynamicRef != "":
			refs = append(refs, obj.DynamicRef)
		}
	}
	if len(refs) != 5 {
		t.Errorf("got %d refs, want 5: %v", len(refs), refs)
	}
}

func TestMetaRefsEarlyStop(t *testing.T) {
	doc, err := jsonschema.Parse([]byte(refsSampleSchema))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for range doc.Refs() {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestMetaRefsOnSchemaWithoutRefs(t *testing.T) {
	doc, err := jsonschema.Parse([]byte(`{"type":"string"}`))
	if err != nil {
		t.Fatal(err)
	}
	for sub := range doc.Refs() {
		t.Errorf("unexpected ref yielded: %#v", sub)
	}
}

func TestMetaRefsOnBooleanSchema(t *testing.T) {
	doc, err := jsonschema.Parse([]byte(`true`))
	if err != nil {
		t.Fatal(err)
	}
	for sub := range doc.Refs() {
		t.Errorf("unexpected ref yielded from bool schema: %#v", sub)
	}
}
