package generate

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// Emit turns an IR Type into a top-level *ast.GenDecl ready for
// printing.
func Emit(t Type) *ast.GenDecl {
	spec := &ast.TypeSpec{
		Name: &ast.Ident{Name: t.Name},
		Type: emitStructType(t),
	}
	decl := &ast.GenDecl{
		Tok:   token.TYPE,
		Specs: []ast.Spec{spec},
	}
	if t.Doc != "" {
		decl.Doc = &ast.CommentGroup{List: []*ast.Comment{{Text: "// " + t.Doc}}}
	}
	return decl
}

func emitStructType(t Type) *ast.StructType {
	fields := &ast.FieldList{}
	for _, f := range t.Fields {
		fields.List = append(fields.List, &ast.Field{
			Names: []*ast.Ident{{Name: f.GoName}},
			Type:  f.TypeExpr,
			Tag:   &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("`json:%s`", strconv.Quote(f.JSONName))},
		})
	}
	return &ast.StructType{Fields: fields}
}
