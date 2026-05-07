package generate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// parseGoTypeExpr parses a Go type expression string into an ast.Expr.
func parseGoTypeExpr(src string) (ast.Expr, error) {
	expr, err := parser.ParseExpr(src)
	if err != nil {
		return nil, fmt.Errorf("parse goType %q: %w", src, err)
	}
	return expr, nil
}

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
			Tag:   jsonStructTag(tagValue),
		})
	}
	for _, af := range t.AdditionalFields {
		field, err := emitAdditionalField(af)
		if err != nil {
			panic(fmt.Errorf("additional field %+v: %w", af, err))
		}
		fields.List = append(fields.List, field)
	}
	return &ast.StructType{Fields: fields}
}

// emitAdditionalField turns an IR GoAdditionalField into an *ast.Field
// for the emitted struct. An empty GoIdent list means embedded (no
// names); a multi-element list emits `a, b, c T`.
//
// Additional fields are wire-invisible by construction: the emitted
// struct tag is always `json:"-"`, so even an exported embedded
// field's promoted members never appear in the marshaled output.
// GoTag is reserved for non-JSON tags (e.g. `db:"…"`) and is
// merged in alongside the json:"-" entry.
func emitAdditionalField(af GoAdditionalField) (*ast.Field, error) {
	typeExpr, err := parseGoTypeExpr(af.GoType)
	if err != nil {
		return nil, err
	}
	field := &ast.Field{Type: typeExpr}
	for _, name := range af.GoIdent {
		field.Names = append(field.Names, &ast.Ident{Name: name})
	}
	tag := `json:"-"`
	if af.GoTag != "" {
		tag = `json:"-" ` + af.GoTag
	}
	field.Tag = &ast.BasicLit{Kind: token.STRING, Value: "`" + tag + "`"}
	if af.GoDoc != "" {
		field.Doc = &ast.CommentGroup{List: []*ast.Comment{{Text: "// " + af.GoDoc}}}
	}
	return field, nil
}
