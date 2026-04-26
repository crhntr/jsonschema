package generate

import (
	"go/ast"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestDerive_Composite_StringOrInteger(t *testing.T) {
	src := `{"type":["string","integer"]}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	typ, err := Derive("Foo", &obj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(typ.Variants) != 2 {
		t.Fatalf("len(Variants) = %d, want 2", len(typ.Variants))
	}

	wantKinds := []string{"string", "integer"}
	wantGoTypes := []string{"string", "int"}
	for i, v := range typ.Variants {
		if v.Kind != wantKinds[i] {
			t.Errorf("Variants[%d].Kind = %q, want %q", i, v.Kind, wantKinds[i])
		}
		id, ok := v.GoTypeExpr.(*ast.Ident)
		if !ok {
			t.Errorf("Variants[%d].GoTypeExpr = %T, want *ast.Ident", i, v.GoTypeExpr)
			continue
		}
		if id.Name != wantGoTypes[i] {
			t.Errorf("Variants[%d].GoTypeExpr.Name = %q, want %q", i, id.Name, wantGoTypes[i])
		}
	}
}
