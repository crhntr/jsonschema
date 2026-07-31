package jsonschema_test

import (
	"testing"
	"testing/fstest"

	"github.com/crhntr/jsonschema"
)

func TestResolverLoad(t *testing.T) {
	body := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "https://example.com/loaded",
		"type": "object",
		"properties": {
			"name": { "type": "string" }
		}
	}`)

	var r jsonschema.Resolver
	doc, err := r.Load("https://example.com/loaded", body)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.BaseURI(); got != "https://example.com/loaded" {
		t.Errorf("BaseURI = %q, want example.com/loaded", got)
	}
	// Resolve must not perform HTTP — the fetch would fail since the
	// resolver has no client wired and no network in tests.
	if _, err := r.Resolve(t.Context(), "https://example.com/loaded"); err != nil {
		t.Fatalf("Resolve after Load: %v", err)
	}
}

func TestResolverLoadFS(t *testing.T) {
	fsys := fstest.MapFS{
		"a.json": &fstest.MapFile{Data: []byte(`{
			"$id": "https://example.com/a",
			"type": "object",
			"properties": { "next": { "$ref": "https://example.com/b" } }
		}`)},
		"b.json": &fstest.MapFile{Data: []byte(`{
			"$id": "https://example.com/b",
			"type": "string"
		}`)},
	}

	var r jsonschema.Resolver
	if err := r.LoadFS(fsys, "*.json"); err != nil {
		t.Fatal(err)
	}
	doc, err := r.Resolve(t.Context(), "https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	next, ok := doc.TypeObject()
	if !ok {
		t.Fatal("expected object")
	}
	target := next.Properties["next"].Resolved()
	if target == nil {
		t.Fatal("Resolved() is nil")
	}
	if target.BaseURI() != "https://example.com/b" {
		t.Errorf("target BaseURI = %q, want example.com/b", target.BaseURI())
	}
}

func TestResolverLoadFSMissingID(t *testing.T) {
	fsys := fstest.MapFS{
		"no-id.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	}
	var r jsonschema.Resolver
	if err := r.LoadFS(fsys, "*.json"); err == nil {
		t.Error("expected error for schema without $id")
	}
}

func TestResolverLoadFSPatternMatchesNothing(t *testing.T) {
	var r jsonschema.Resolver
	err := r.LoadFS(fstest.MapFS{}, "*.json")
	if err == nil {
		t.Error("expected error when pattern matches no files")
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

func TestResolveRefIntoUnknownKeyword(t *testing.T) {
	// Draft-07-style documents keep subschemas under "definitions",
	// which 2020-12 does not recognize; the whole subtree lands in
	// SchemaObject.Extra. A $ref pointing inside it must still
	// resolve to a parsed schema.
	t.Run("ref one level into definitions", func(t *testing.T) {
		body := []byte(`{
			"$id": "https://example.com/legacy",
			"definitions": {
				"count": { "type": "integer" }
			},
			"items": { "$ref": "#/definitions/count" }
		}`)

		var r jsonschema.Resolver
		if _, err := r.Load("https://example.com/legacy", body); err != nil {
			t.Fatal(err)
		}
		schema, err := r.Resolve(t.Context(), "https://example.com/legacy")
		if err != nil {
			t.Fatal(err)
		}

		items := mustObject(t, schema).Items
		if items == nil {
			t.Fatal("items missing")
		}
		target := items.Resolved()
		if target == nil {
			t.Fatal("items.Resolved() is nil")
		}
		tobj := mustObject(t, target)
		if tobj.Type == nil {
			t.Fatal("target type is nil")
		}
		if got, _ := tobj.Type.TypeString(); string(got) != "integer" {
			t.Errorf("target type = %q, want integer", got)
		}
	})

	t.Run("nested ref inside lazily parsed schema links", func(t *testing.T) {
		// Mirrors draft-07's schemaArray definition: the definition
		// itself contains a $ref back to the resource root. The
		// lazily parsed subtree must be linked like any other.
		body := []byte(`{
			"$id": "https://example.com/legacy-nested",
			"definitions": {
				"schemaArray": {
					"type": "array",
					"items": { "$ref": "#" }
				}
			},
			"items": { "$ref": "#/definitions/schemaArray" }
		}`)

		var r jsonschema.Resolver
		if _, err := r.Load("https://example.com/legacy-nested", body); err != nil {
			t.Fatal(err)
		}
		schema, err := r.Resolve(t.Context(), "https://example.com/legacy-nested")
		if err != nil {
			t.Fatal(err)
		}

		target := mustObject(t, schema).Items.Resolved()
		if target == nil {
			t.Fatal("items.Resolved() is nil")
		}
		inner := mustObject(t, target).Items
		if inner == nil {
			t.Fatal("lazily parsed schema lost its items subschema")
		}
		if inner.Resolved() != schema {
			t.Errorf("inner items.Resolved() = %p, want the resource root %p", inner.Resolved(), schema)
		}
	})

	t.Run("two refs to the same definition share one schema", func(t *testing.T) {
		body := []byte(`{
			"$id": "https://example.com/legacy-shared",
			"definitions": {
				"count": { "type": "integer" }
			},
			"properties": {
				"a": { "$ref": "#/definitions/count" },
				"b": { "$ref": "#/definitions/count" }
			}
		}`)

		var r jsonschema.Resolver
		if _, err := r.Load("https://example.com/legacy-shared", body); err != nil {
			t.Fatal(err)
		}
		schema, err := r.Resolve(t.Context(), "https://example.com/legacy-shared")
		if err != nil {
			t.Fatal(err)
		}

		props := mustObject(t, schema).Properties
		a, b := props["a"].Resolved(), props["b"].Resolved()
		if a == nil || b == nil {
			t.Fatalf("Resolved() = %p, %p, want both non-nil", a, b)
		}
		if a != b {
			t.Errorf("refs to the same pointer resolved to distinct schemas %p != %p", a, b)
		}
	})

	t.Run("mutually referencing definitions terminate", func(t *testing.T) {
		body := []byte(`{
			"$id": "https://example.com/legacy-cycle",
			"definitions": {
				"a": { "items": { "$ref": "#/definitions/b" } },
				"b": { "items": { "$ref": "#/definitions/a" } }
			},
			"items": { "$ref": "#/definitions/a" }
		}`)

		var r jsonschema.Resolver
		if _, err := r.Load("https://example.com/legacy-cycle", body); err != nil {
			t.Fatal(err)
		}
		schema, err := r.Resolve(t.Context(), "https://example.com/legacy-cycle")
		if err != nil {
			t.Fatal(err)
		}

		a := mustObject(t, schema).Items.Resolved()
		if a == nil {
			t.Fatal("items.Resolved() is nil")
		}
		b := mustObject(t, a).Items.Resolved()
		if b == nil {
			t.Fatal("a items.Resolved() is nil")
		}
		if got := mustObject(t, b).Items.Resolved(); got != a {
			t.Errorf("cycle b -> a resolved to %p, want the memoized a %p", got, a)
		}
	})

	t.Run("ref terminating exactly at an unknown keyword links too", func(t *testing.T) {
		body := []byte(`{
			"$id": "https://example.com/legacy-direct",
			"x-legacy": {
				"type": "array",
				"items": { "$ref": "#" }
			},
			"items": { "$ref": "#/x-legacy" }
		}`)

		var r jsonschema.Resolver
		if _, err := r.Load("https://example.com/legacy-direct", body); err != nil {
			t.Fatal(err)
		}
		schema, err := r.Resolve(t.Context(), "https://example.com/legacy-direct")
		if err != nil {
			t.Fatal(err)
		}

		target := mustObject(t, schema).Items.Resolved()
		if target == nil {
			t.Fatal("items.Resolved() is nil")
		}
		inner := mustObject(t, target).Items
		if inner == nil {
			t.Fatal("lazily parsed schema lost its items subschema")
		}
		if inner.Resolved() != schema {
			t.Errorf("inner items.Resolved() = %p, want the resource root %p", inner.Resolved(), schema)
		}
	})
}

// TestResolveDraft07MetaSchema covers resolving a schema whose $schema
// declares the draft-07 dialect. The resolver fetches the draft-07
// meta-schema, whose internal $refs all point into "definitions" — an
// unknown keyword in 2020-12 — and must still link it rather than
// failing the user's resolution.
func TestResolveDraft07MetaSchema(t *testing.T) {
	_, client := startSchemaServer(t)
	schema, err := jsonschema.Resolve(t.Context(), client, "https://example.com/refs/draft-07")
	if err != nil {
		t.Fatal(err)
	}

	if out := schema.Validate("ok.json", []byte(`{"name": "a"}`)); !out.Valid {
		t.Errorf("Validate(valid instance) = invalid, want valid:\n%v", out.AsError())
	}
	if out := schema.Validate("bad.json", []byte(`{"name": 1}`)); out.Valid {
		t.Error("Validate(invalid instance) = valid, want invalid")
	}
}
