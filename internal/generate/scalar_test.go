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

func TestDerive_IntegerScalarWithRange(t *testing.T) {
	src := `{"type":"integer","minimum":1,"maximum":150}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	typ, err := Derive("Age", &obj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	id, ok := typ.Underlying.(*ast.Ident)
	if !ok || id.Name != "int" {
		t.Errorf("Underlying = %#v, want *ast.Ident{Name:\"int\"}", typ.Underlying)
	}
	if typ.Constraints.Minimum == nil || *typ.Constraints.Minimum != "1" {
		t.Errorf("Minimum = %v, want \"1\"", typ.Constraints.Minimum)
	}
	if typ.Constraints.Maximum == nil || *typ.Constraints.Maximum != "150" {
		t.Errorf("Maximum = %v, want \"150\"", typ.Constraints.Maximum)
	}
}

func TestDerive_IntegerScalarRejectsNonIntegerBound(t *testing.T) {
	src := `{"type":"integer","minimum":1.5}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	_, err = Derive("Age", &obj)
	if err == nil {
		t.Fatal("Derive: nil error, want non-integer minimum rejection")
	}
	if !strings.Contains(err.Error(), "minimum") {
		t.Errorf("err = %q, want mention of minimum", err)
	}
}

func TestDerive_IntegerScalarRejectsOverflowBound(t *testing.T) {
	src := `{"type":"integer","maximum":99999999999999999999}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	_, err = Derive("Big", &obj)
	if err == nil {
		t.Fatal("Derive: nil error, want overflow rejection")
	}
	if !strings.Contains(err.Error(), "maximum") {
		t.Errorf("err = %q, want mention of maximum", err)
	}
}

func TestDerive_NumberScalarAcceptsFloatBound(t *testing.T) {
	src := `{"type":"number","minimum":1.5,"maximum":2.75}`
	s, err := jsonschema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	obj, _ := s.TypeObject()
	if _, err := Derive("Ratio", &obj); err != nil {
		t.Fatalf("Derive: %v", err)
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
