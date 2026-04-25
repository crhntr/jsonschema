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
		`func (r User) MarshalJSONTo(enc *jsontext.Encoder, opts ...json.Options) error`,
		`func (r *User) UnmarshalJSONFrom(dec *jsontext.Decoder, opts ...json.Options) error`,
		`json.RejectUnknownMembers(true)`,
		`shadow.Name == nil`,
		`fmt.Errorf("missing required field %q", "name")`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source missing %q\nsrc:\n%s", want, src)
		}
	}
}
