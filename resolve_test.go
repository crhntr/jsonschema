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
