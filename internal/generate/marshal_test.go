package generate

import (
	"go/ast"
	"strings"
	"testing"
)

func TestEmitMarshal_Skeleton(t *testing.T) {
	typ := Type{
		Name: "User",
		Fields: []Field{
			{GoName: "Name", JSONName: "name", TypeExpr: &ast.Ident{Name: "string"}, Required: true},
			{GoName: "Nickname", JSONName: "nickname", TypeExpr: &ast.StarExpr{X: &ast.Ident{Name: "string"}}, Required: false},
		},
		RejectUnknown: true,
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
		`func (r User) MarshalJSONTo(enc *jsontext.Encoder) error`,
		`func (r *User) UnmarshalJSONFrom(dec *jsontext.Decoder) error`,
		`json.RejectUnknownMembers(true)`,
		`shadow.Name == nil`,
		`fmt.Errorf("missing required field %q", "name")`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source missing %q\nsrc:\n%s", want, src)
		}
	}
}

// TestEmitMarshal_ManualPathOptionalCollection guards against the
// manual struct-marshal path (triggered by NullProperties) treating
// optional slice/map fields as pointers. Optional collections are
// emitted as bare slices/maps — dereferencing them would emit
// `*r.Field`, which does not compile.
func TestEmitMarshal_ManualPathOptionalCollection(t *testing.T) {
	typ := Type{
		Name: "Record",
		Fields: []Field{
			{
				GoName:   "Tags",
				JSONName: "tags",
				TypeExpr: &ast.ArrayType{Elt: &ast.Ident{Name: "string"}},
				Required: false,
			},
			{
				GoName:   "Attrs",
				JSONName: "attrs",
				TypeExpr: &ast.MapType{Key: &ast.Ident{Name: "string"}, Value: &ast.Ident{Name: "int"}},
				Required: false,
			},
			{
				GoName:   "Note",
				JSONName: "note",
				TypeExpr: &ast.StarExpr{X: &ast.Ident{Name: "string"}},
				Required: false,
			},
		},
		NullProperties: []NullProperty{{JSONName: "marker", Required: true}},
	}
	src, err := formatFile("model", []ast.Decl{
		Emit(typ),
		EmitMarshal(typ),
	})
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	if strings.Contains(src, "*r.Tags") {
		t.Errorf("optional slice field should not be dereferenced\nsrc:\n%s", src)
	}
	if strings.Contains(src, "*r.Attrs") {
		t.Errorf("optional map field should not be dereferenced\nsrc:\n%s", src)
	}
	if !strings.Contains(src, "*r.Note") {
		t.Errorf("optional pointer field should be dereferenced\nsrc:\n%s", src)
	}
}
