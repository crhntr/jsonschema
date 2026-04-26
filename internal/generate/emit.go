package generate

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// Emit turns an IR Type into a top-level *ast.GenDecl ready for
// printing. Scalar types emit `type Name <expr>`; struct types emit
// `type Name struct { … }`; composite types emit a discriminated
// union struct.
func Emit(t Type) *ast.GenDecl {
	var typExpr ast.Expr
	switch {
	case len(t.Variants) > 0:
		typExpr = emitCompositeStructType(t)
	case t.Underlying != nil:
		typExpr = t.Underlying
	default:
		typExpr = emitStructType(t)
	}
	spec := &ast.TypeSpec{
		Name: &ast.Ident{Name: t.Name},
		Type: typExpr,
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
	fields := new(ast.FieldList)
	for _, f := range t.Fields {
		tagValue := f.JSONName
		extras := f.JSONTags
		if !f.Required && len(extras) == 0 {
			extras = []string{"omitzero"}
		}
		for _, e := range extras {
			tagValue += "," + e
		}
		fields.List = append(fields.List, &ast.Field{
			Names: []*ast.Ident{{Name: f.GoName}},
			Type:  f.TypeExpr,
			Tag:   &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("`json:%s`", strconv.Quote(tagValue))},
		})
	}
	return &ast.StructType{Fields: fields}
}
