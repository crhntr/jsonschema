package generate

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestDerive_StringScalarWithLengthBounds(t *testing.T) {
	src := `{"type":"string","minLength":3,"maxLength":12}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	typ, err := Derive("Username", &obj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if typ.Underlying == nil {
		t.Fatalf("Underlying = nil, want *ast.Ident{string}")
	}
	id, ok := typ.Underlying.(*ast.Ident)
	if !ok || id.Name != "string" {
		t.Errorf("Underlying = %#v, want *ast.Ident{Name:\"string\"}", typ.Underlying)
	}
	if typ.Constraints.MinLength == nil || *typ.Constraints.MinLength != 3 {
		t.Errorf("MinLength = %v, want 3", typ.Constraints.MinLength)
	}
	if typ.Constraints.MaxLength == nil || *typ.Constraints.MaxLength != 12 {
		t.Errorf("MaxLength = %v, want 12", typ.Constraints.MaxLength)
	}
	if len(typ.Fields) != 0 {
		t.Errorf("Fields = %v, want none", typ.Fields)
	}
}

func TestEmit_ScalarStringAlias(t *testing.T) {
	min, max := 3, 12
	typ := Type{
		Name:       "Username",
		Underlying: &ast.Ident{Name: "string"},
		Constraints: Constraints{
			MinLength: &min,
			MaxLength: &max,
		},
	}
	src, err := formatFile("model", []ast.Decl{
		Emit(typ),
		EmitMarshal(typ),
		EmitUnmarshal(typ),
	})
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	for _, want := range []string{
		"type Username string",
		"func (r Username) MarshalJSONTo(",
		"func (r *Username) UnmarshalJSONFrom(",
		`len(v) < 3`,
		`len(v) > 12`,
		`*r = Username(v)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source missing %q\nsrc:\n%s", want, src)
		}
	}
}
