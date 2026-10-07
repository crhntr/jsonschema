package jsonhandler

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRoundTrip renders a form for a value, submits the controls the way a
// browser would, and expects the same value back.
func TestRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, schema, value string
		want                string // when the form normalizes the value; defaults to value
	}{
		{
			name:   "object of scalars",
			schema: `{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"age":{"type":"integer"},"height":{"type":"number"},"admin":{"type":"boolean"}}}`,
			value:  `{"name":"Ada","age":36,"height":1.65,"admin":true}`,
		},
		{
			name:   "required false boolean",
			schema: `{"type":"object","required":["on"],"properties":{"on":{"type":"boolean"}}}`,
			value:  `{"on":false}`,
		},
		{
			name:   "optional false boolean is left out",
			schema: `{"type":"object","properties":{"on":{"type":"boolean"}}}`,
			value:  `{"on":false}`,
			want:   `{}`,
		},
		{
			name:   "optional false boolean defaulting to true",
			schema: `{"type":"object","properties":{"on":{"type":"boolean","default":true}}}`,
			value:  `{"on":false}`,
		},
		{
			name:   "nested objects",
			schema: `{"type":"object","properties":{"address":{"type":"object","properties":{"street":{"type":"string"},"geo":{"type":"object","properties":{"lat":{"type":"number"}}}}}}}`,
			value:  `{"address":{"street":"Main","geo":{"lat":1.5}}}`,
		},
		{
			name:   "empty optional object kept when present",
			schema: `{"type":"object","properties":{"meta":{"type":"object","properties":{"a":{"type":"string"}}}}}`,
			value:  `{"meta":{}}`,
		},
		{
			name:   "array of strings",
			schema: `{"type":"array","items":{"type":"string"}}`,
			value:  `["a","b",""]`,
		},
		{
			name:   "array of objects",
			schema: `{"type":"array","items":{"type":"object","properties":{"n":{"type":"integer"}}}}`,
			value:  `[{"n":1},{"n":2}]`,
		},
		{
			name:   "empty array",
			schema: `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`,
			value:  `{"tags":[]}`,
		},
		{
			name:   "tuple",
			schema: `{"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}],"items":false}`,
			value:  `["x",3]`,
		},
		{
			name:   "string enum",
			schema: `{"type":"object","properties":{"color":{"enum":["red","green"]}}}`,
			value:  `{"color":"green"}`,
		},
		{
			name:   "mixed enum",
			schema: `{"enum":[1,"one",null,{"a":true}]}`,
			value:  `{"a":true}`,
		},
		{
			name:   "radio enum",
			schema: `{"type":"object","required":["size"],"properties":{"size":{"enum":["s","m","l"],"x-form":{"widget":"radio"}}}}`,
			value:  `{"size":"m"}`,
		},
		{
			name:   "checkboxes",
			schema: `{"type":"array","uniqueItems":true,"items":{"enum":["a","b","c"]}}`,
			value:  `["a","c"]`,
		},
		{
			name:   "empty checkboxes",
			schema: `{"type":"array","uniqueItems":true,"items":{"enum":["a","b","c"]}}`,
			value:  `[]`,
		},
		{
			name:   "const",
			schema: `{"type":"object","required":["kind"],"properties":{"kind":{"const":"dog"}}}`,
			value:  `{"kind":"dog"}`,
		},
		{
			name:   "nullable string null",
			schema: `{"type":"object","required":["nick"],"properties":{"nick":{"type":["string","null"]}}}`,
			value:  `{"nick":null}`,
		},
		{
			name:   "nullable string",
			schema: `{"type":"object","required":["nick"],"properties":{"nick":{"type":["string","null"]}}}`,
			value:  `{"nick":"x"}`,
		},
		{
			name:   "nullable object null",
			schema: `{"type":["object","null"],"properties":{"a":{"type":"string"}}}`,
			value:  `null`,
		},
		{
			name:   "union of types",
			schema: `{"type":"object","properties":{"v":{"type":["string","number"]}}}`,
			value:  `{"v":3.5}`,
		},
		{
			name:   "oneOf discriminated",
			schema: `{"oneOf":[{"title":"Dog","type":"object","required":["kind"],"properties":{"kind":{"const":"dog"},"barks":{"type":"boolean"}}},{"title":"Cat","type":"object","required":["kind"],"properties":{"kind":{"const":"cat"},"lives":{"type":"integer"}}}]}`,
			value:  `{"kind":"cat","lives":9}`,
		},
		{
			name:   "anyOf by type",
			schema: `{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}}]}`,
			value:  `["x"]`,
		},
		{
			name:   "map",
			schema: `{"type":"object","additionalProperties":{"type":"integer"}}`,
			value:  `{"a":1,"b":2}`,
		},
		{
			name:   "free-form object",
			schema: `{"type":"object"}`,
			value:  `{"a":[1,{"b":null}],"c":"d"}`,
		},
		{
			name:   "undeclared properties are kept",
			schema: `{"type":"object","properties":{"a":{"type":"string"}}}`,
			value:  `{"a":"x","z":[true]}`,
		},
		{
			name:   "properties and additionalProperties",
			schema: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"boolean"}}`,
			value:  `{"a":"x","b":true,"c":false}`,
		},
		{
			name:   "any value",
			schema: `true`,
			value:  `{"anything":[1,2,3]}`,
		},
		{
			name:   "date-time",
			schema: `{"type":"string","format":"date-time"}`,
			value:  `"2024-01-02T03:04:05Z"`,
		},
		{
			name:   "time",
			schema: `{"type":"string","format":"time"}`,
			value:  `"03:04:05Z"`,
		},
		{
			name:   "date",
			schema: `{"type":"string","format":"date"}`,
			value:  `"2024-01-02"`,
		},
		{
			name:   "textarea",
			schema: `{"type":"string","x-form":{"widget":"textarea"}}`,
			value:  `"line one\nline two"`,
		},
		{
			name:   "allOf merges properties",
			schema: `{"allOf":[{"type":"object","properties":{"a":{"type":"string"}}},{"properties":{"b":{"type":"integer"}}}]}`,
			value:  `{"a":"x","b":2}`,
		},
		{
			name:   "ref",
			schema: `{"$defs":{"point":{"type":"object","properties":{"x":{"type":"number"},"y":{"type":"number"}}}},"type":"object","properties":{"from":{"$ref":"#/$defs/point"},"to":{"$ref":"#/$defs/point"}}}`,
			value:  `{"from":{"x":1,"y":2},"to":{"x":3,"y":4}}`,
		},
		{
			name:   "recursive",
			schema: `{"$defs":{"node":{"type":"object","properties":{"name":{"type":"string"},"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`,
			value:  `{"name":"a","next":{"name":"b","next":{"name":"c"}}}`,
		},
		{
			name:   "unicode property names",
			schema: `{"type":"object","additionalProperties":{"type":"string"}}`,
			value:  `{"a/b":"1","~x":"2","ünï":"3","~k0":"4"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, tc.schema)
			h.Value = jsontext.Value(tc.value)
			got := submit(t, h, formState(t, getForm(t, h)))
			want := tc.want
			if want == "" {
				want = tc.value
			}
			assertJSONEqual(t, got, want)
		})
	}
}

// sortTypeArrays sorts the array value of every "type" member.
func sortTypeArrays(t *testing.T, v []byte) jsontext.Value {
	t.Helper()
	var doc any
	if err := json.Unmarshal(v, &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if types, ok := v["type"].([]any); ok {
				slices.SortFunc(types, func(a, b any) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRoundTripMetaSchema edits each 2020-12 meta-schema document through
// the form generated from the meta-schema itself.
func TestRoundTripMetaSchema(t *testing.T) {
	h := metaHandler(t)
	root := filepath.Join("..", "internal", "metaschema", "draft", "2020-12")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		t.Run(path, func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			h.Value = body
			got := submit(t, h, formState(t, getForm(t, h)))
			// Checkbox groups submit in option order, so a type array
			// comes back in the meta-schema's enum order.
			assertJSONEqual(t, sortTypeArrays(t, got), string(sortTypeArrays(t, body)))
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
