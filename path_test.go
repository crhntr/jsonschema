package jsonschema_test

import (
	"testing"

	"github.com/crhntr/jsonschema"
)

// resolveBytes is a small helper: Load + Resolve with a Resolver that has
// no HTTP client (every $ref must already be loaded). Used by the
// PathInResource tests below.
func resolveBytes(t *testing.T, uri string, body []byte) *jsonschema.Schema {
	t.Helper()
	var r jsonschema.Resolver
	if _, err := r.Load(uri, body); err != nil {
		t.Fatalf("Load: %v", err)
	}
	doc, err := r.Resolve(t.Context(), uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return doc
}

func TestPathInResourceRoot(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/root", []byte(`{
		"$id": "https://example.com/root",
		"type": "object"
	}`))
	if got := doc.PathInResource(); got != "" {
		t.Errorf("root PathInResource = %q, want \"\"", got)
	}
}

func TestPathInResourceObjectKeywords(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/obj", []byte(`{
		"$id": "https://example.com/obj",
		"properties": {
			"foo": { "type": "string" },
			"bar": { "items": { "type": "integer" } }
		},
		"patternProperties": {
			"^x": { "type": "boolean" }
		},
		"$defs": {
			"alpha": { "type": "string" }
		},
		"additionalProperties": { "type": "null" },
		"propertyNames": { "minLength": 1 }
	}`))
	obj := mustObject(t, doc)

	for _, tc := range []struct {
		name string
		got  *jsonschema.Schema
		want string
	}{
		{"properties.foo", obj.Properties["foo"], "/properties/foo"},
		{"properties.bar", obj.Properties["bar"], "/properties/bar"},
		{"properties.bar.items", mustObject(t, obj.Properties["bar"]).Items, "/properties/bar/items"},
		{"patternProperties.^x", obj.PatternProperties["^x"], "/patternProperties/^x"},
		{"$defs.alpha", obj.Defs["alpha"], "/$defs/alpha"},
		{"additionalProperties", obj.AdditionalProperties, "/additionalProperties"},
		{"propertyNames", obj.PropertyNames, "/propertyNames"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == nil {
				t.Fatalf("subschema %s missing", tc.name)
			}
			if got := tc.got.PathInResource(); got != tc.want {
				t.Errorf("PathInResource = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPathInResourceArrayKeywords(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/arr", []byte(`{
		"$id": "https://example.com/arr",
		"allOf": [
			{ "type": "object" },
			{ "type": "array" }
		],
		"anyOf": [
			{ "type": "string" }
		],
		"oneOf": [
			{ "minimum": 0 },
			{ "maximum": 0 }
		],
		"prefixItems": [
			{ "type": "string" },
			{ "type": "integer" }
		]
	}`))
	obj := mustObject(t, doc)

	for _, tc := range []struct {
		name string
		got  *jsonschema.Schema
		want string
	}{
		{"allOf[0]", obj.AllOf[0], "/allOf/0"},
		{"allOf[1]", obj.AllOf[1], "/allOf/1"},
		{"anyOf[0]", obj.AnyOf[0], "/anyOf/0"},
		{"oneOf[0]", obj.OneOf[0], "/oneOf/0"},
		{"oneOf[1]", obj.OneOf[1], "/oneOf/1"},
		{"prefixItems[0]", obj.PrefixItems[0], "/prefixItems/0"},
		{"prefixItems[1]", obj.PrefixItems[1], "/prefixItems/1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got.PathInResource(); got != tc.want {
				t.Errorf("PathInResource = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPathInResourceConditionalKeywords(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/cond", []byte(`{
		"$id": "https://example.com/cond",
		"if":   { "type": "string" },
		"then": { "minLength": 1 },
		"else": { "type": "integer" },
		"not":  { "type": "null" }
	}`))
	obj := mustObject(t, doc)

	for keyword, sub := range map[string]*jsonschema.Schema{
		"if":   obj.If,
		"then": obj.Then,
		"else": obj.Else,
		"not":  obj.Not,
	} {
		want := "/" + keyword
		if got := sub.PathInResource(); got != want {
			t.Errorf("PathInResource(%s) = %q, want %q", keyword, got, want)
		}
	}
}

func TestPathInResourceEmbeddedID(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/outer", []byte(`{
		"$id": "https://example.com/outer",
		"$defs": {
			"inner": {
				"$id": "https://example.com/outer/inner",
				"properties": {
					"x": { "type": "string" }
				}
			}
		}
	}`))
	inner := mustObject(t, doc).Defs["inner"]

	// inner is itself a resource root: its path within ITS resource is "".
	if got := inner.PathInResource(); got != "" {
		t.Errorf("embedded inner PathInResource = %q, want \"\"", got)
	}
	// x is at /properties/x within the inner resource (not under /$defs/inner).
	x := mustObject(t, inner).Properties["x"]
	if got := x.PathInResource(); got != "/properties/x" {
		t.Errorf("inner.properties.x PathInResource = %q, want /properties/x", got)
	}
}

func TestPathInResourceEscaped(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/esc", []byte(`{
		"$id": "https://example.com/esc",
		"properties": {
			"a/b": { "type": "string" },
			"c~d": { "type": "integer" }
		}
	}`))
	obj := mustObject(t, doc)
	if got := obj.Properties["a/b"].PathInResource(); got != "/properties/a~1b" {
		t.Errorf("a/b PathInResource = %q, want /properties/a~1b", got)
	}
	if got := obj.Properties["c~d"].PathInResource(); got != "/properties/c~0d" {
		t.Errorf("c~d PathInResource = %q, want /properties/c~0d", got)
	}
}
