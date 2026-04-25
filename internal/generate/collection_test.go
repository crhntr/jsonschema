package generate

import (
	"go/ast"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestDerive_ArrayOfStrings(t *testing.T) {
	src := `{"type":"array","items":{"type":"string"}}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	typ, err := Derive("Tags", &obj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	arr, ok := typ.Underlying.(*ast.ArrayType)
	if !ok {
		t.Fatalf("Underlying = %T, want *ast.ArrayType", typ.Underlying)
	}
	id, ok := arr.Elt.(*ast.Ident)
	if !ok || id.Name != "string" {
		t.Errorf("Underlying.Elt = %#v, want *ast.Ident{Name:\"string\"}", arr.Elt)
	}
}

func TestDerive_MapOfStringToInt(t *testing.T) {
	src := `{"type":"object","additionalProperties":{"type":"integer"}}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	typ, err := Derive("Counts", &obj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	m, ok := typ.Underlying.(*ast.MapType)
	if !ok {
		t.Fatalf("Underlying = %T, want *ast.MapType", typ.Underlying)
	}
	if id, ok := m.Key.(*ast.Ident); !ok || id.Name != "string" {
		t.Errorf("map key = %#v, want *ast.Ident{Name:\"string\"}", m.Key)
	}
	if id, ok := m.Value.(*ast.Ident); !ok || id.Name != "int" {
		t.Errorf("map value = %#v, want *ast.Ident{Name:\"int\"}", m.Value)
	}
}
