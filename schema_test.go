package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

func mustObject(t *testing.T, m *jsonschema.Meta) jsonschema.MetaObject {
	t.Helper()
	obj, ok := m.TypeObject()
	if !ok {
		t.Fatalf("expected object schema, got bool")
	}
	return obj
}

// findResolved walks schema.allOf for a $ref containing pathFragment and
// returns the resolved target (or nil).
func findResolved(t *testing.T, schema *jsonschema.Meta, pathFragment string) *jsonschema.Meta {
	t.Helper()
	obj := mustObject(t, schema)
	for i := range obj.AllOf {
		sub := &obj.AllOf[i]
		ref := mustObject(t, sub).Ref
		if strings.Contains(ref, pathFragment) {
			return sub.Resolved()
		}
	}
	return nil
}

// findFirstDynamicRef walks the subtree under m looking for the first Meta
// whose MetaObject has a non-empty $dynamicRef. Used to spot-check
// bookending of $dynamicRef.
func findFirstDynamicRef(m *jsonschema.Meta) *jsonschema.Meta {
	if m == nil {
		return nil
	}
	obj, ok := m.TypeObject()
	if !ok {
		return nil
	}
	if obj.DynamicRef != "" {
		return m
	}
	for _, c := range obj.Defs {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	for _, c := range obj.Properties {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	for i := range obj.AllOf {
		if r := findFirstDynamicRef(&obj.AllOf[i]); r != nil {
			return r
		}
	}
	for i := range obj.AnyOf {
		if r := findFirstDynamicRef(&obj.AnyOf[i]); r != nil {
			return r
		}
	}
	for i := range obj.OneOf {
		if r := findFirstDynamicRef(&obj.OneOf[i]); r != nil {
			return r
		}
	}
	for _, c := range []*jsonschema.Meta{obj.If, obj.Then, obj.Else, obj.Not, obj.Items, obj.AdditionalProperties, obj.PropertyNames} {
		if r := findFirstDynamicRef(c); r != nil {
			return r
		}
	}
	return nil
}

func TestMeta(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entrypoint string
		minSchemas int
	}{
		{
			name:       "2020-12 meta-schema",
			entrypoint: "https://json-schema.org/draft/2020-12/schema",
			minSchemas: 8, // schema.json + 7 vocab files
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := startSchemaServer(t)
			schema, err := jsonschema.Resolve(t.Context(), client, tc.entrypoint)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.entrypoint, err)
			}
			if schema == nil {
				t.Fatal("Resolve returned nil schema")
			}
			if got := schema.BaseURI(); got != tc.entrypoint {
				t.Errorf("BaseURI = %q, want %q", got, tc.entrypoint)
			}
			if len(schema.Source()) == 0 {
				t.Errorf("Source() empty; want bytes from response")
			}

			// Every $ref in allOf should resolve to a non-nil target whose
			// BaseURI matches the referenced meta/* document.
			obj, ok := schema.TypeObject()
			if !ok {
				t.Fatal("schema is not an object")
			}
			if len(obj.AllOf) == 0 {
				t.Fatal("schema.allOf is empty")
			}
			for i := range obj.AllOf {
				sub := &obj.AllOf[i]
				resolved := sub.Resolved()
				if resolved == nil {
					t.Errorf("allOf[%d] %q: Resolved() is nil", i, mustObject(t, sub).Ref)
					continue
				}
				if resolved.BaseURI() == "" {
					t.Errorf("allOf[%d]: target BaseURI empty", i)
				}
			}

			// Verify $dynamicRef "#meta" inside meta/applicator is bookended.
			applicator := findResolved(t, schema, "meta/applicator")
			if applicator == nil {
				t.Fatal("could not find meta/applicator via allOf")
			}
			dyn := findFirstDynamicRef(applicator)
			if dyn == nil {
				t.Fatal("no $dynamicRef found in applicator subtree")
			}
			if !dyn.IsDynamic() {
				t.Errorf("$dynamicRef %q: expected IsDynamic() true", mustObject(t, dyn).DynamicRef)
			}
			if dyn.Resolved() == nil {
				t.Fatal("$dynamicRef Resolved() is nil")
			}
			// Lexical fallback target's resource should declare the
			// $dynamicAnchor we're looking up.
			anchorName := strings.TrimPrefix(mustObject(t, dyn).DynamicRef, "#")
			if dyn.Resolved().Resource().DynamicAnchor(anchorName) == nil {
				t.Errorf("target resource missing $dynamicAnchor %q", anchorName)
			}
		})
	}
}

func TestResolveJSONPointerFragment(t *testing.T) {
	_, client := startSchemaServer(t)
	schema, err := jsonschema.Resolve(t.Context(), client, "https://example.com/refs/json-pointer")
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObject(t, schema)
	count := obj.Properties["count"]
	if count == nil {
		t.Fatal("properties.count missing")
	}
	target := count.Resolved()
	if target == nil {
		t.Fatal("count.Resolved() is nil")
	}
	tobj := mustObject(t, target)
	if tobj.Type == nil {
		t.Fatal("target type is nil")
	}
	if got, _ := tobj.Type.TypeString(); string(got) != "integer" {
		t.Errorf("target type = %q, want integer", got)
	}
}

func TestResolveAnchorFragment(t *testing.T) {
	_, client := startSchemaServer(t)
	schema, err := jsonschema.Resolve(t.Context(), client, "https://example.com/refs/anchor")
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObject(t, schema)
	name := obj.Properties["name"]
	if name == nil {
		t.Fatal("properties.name missing")
	}
	target := name.Resolved()
	if target == nil {
		t.Fatal("name.Resolved() is nil")
	}
	if got, _ := mustObject(t, target).Type.TypeString(); string(got) != "string" {
		t.Errorf("target type = %q, want string", got)
	}
}

func TestResolveEmbeddedResource(t *testing.T) {
	_, client := startSchemaServer(t)
	schema, err := jsonschema.Resolve(t.Context(), client, "https://example.com/refs/embedded")
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObject(t, schema)
	child := obj.Properties["child"]
	if child == nil {
		t.Fatal("properties.child missing")
	}
	target := child.Resolved()
	if target == nil {
		t.Fatal("child.Resolved() is nil")
	}
	if got := target.BaseURI(); got != "https://example.com/refs/embedded/inner" {
		t.Errorf("target BaseURI = %q, want embedded inner", got)
	}
}

func TestResolveDynamicRef(t *testing.T) {
	_, client := startSchemaServer(t)
	schema, err := jsonschema.Resolve(t.Context(), client, "https://example.com/refs/dynamic")
	if err != nil {
		t.Fatal(err)
	}
	obj := mustObject(t, schema)
	next := obj.Properties["next"]
	if next == nil {
		t.Fatal("properties.next missing")
	}
	if !next.IsDynamic() {
		t.Error("expected $dynamicRef to be bookended (IsDynamic=true)")
	}
	if next.Resolved() != schema {
		t.Error("expected lexical fallback to be the resource root itself")
	}
}
