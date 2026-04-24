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

