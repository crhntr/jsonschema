package generate

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"strconv"

	"golang.org/x/tools/imports"

	"github.com/crhntr/jsonschema"
)

// interfaceAssertions emits compile-time guards that the generated
// type satisfies json.MarshalerTo and json.UnmarshalerFrom. If the
// emitted method signatures ever drift from the interfaces, the
// generated code stops compiling.
func interfaceAssertions(t Type) ast.Decl {
	return &ast.GenDecl{
		Tok:    token.VAR,
		Lparen: 1, Rparen: 1,
		Specs: []ast.Spec{
			interfaceAssertSpec(t.Name, "MarshalerTo"),
			interfaceAssertSpec(t.Name, "UnmarshalerFrom"),
		},
	}
}

func interfaceAssertSpec(typeName, interfaceName string) *ast.ValueSpec {
	return &ast.ValueSpec{
		Names: []*ast.Ident{{Name: "_"}},
		Type: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "json"},
			Sel: &ast.Ident{Name: interfaceName},
		},
		Values: []ast.Expr{
			&ast.CallExpr{
				Fun: &ast.ParenExpr{
					X: &ast.StarExpr{X: &ast.Ident{Name: typeName}},
				},
				Args: []ast.Expr{&ast.Ident{Name: "nil"}},
			},
		},
	}
}

func importDecl(paths ...string) *ast.GenDecl {
	d := &ast.GenDecl{Tok: token.IMPORT, Lparen: 1, Rparen: 1}
	for _, p := range paths {
		d.Specs = append(d.Specs, &ast.ImportSpec{
			Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(p)},
		})
	}
	return d
}

// Generate emits a complete Go source file for the given schema. The
// resulting bytes have been run through goimports so unused imports
// from the generation templates are pruned and used ones added.
func Generate(schema *jsonschema.SchemaObject, typeName, packageName string) ([]byte, error) {
	typ, err := Derive(typeName, schema)
	if err != nil {
		return nil, fmt.Errorf("derive %s: %w", typeName, err)
	}

	decls := []ast.Decl{
		importDecl(
			"encoding/json/v2",
			"encoding/json/jsontext",
			"fmt",
			"regexp",
		),
		Emit(typ),
	}
	if pat := EmitPatternVar(typ); pat != nil {
		decls = append(decls, pat)
	}
	decls = append(decls,
		EmitMarshal(typ),
		EmitUnmarshal(typ),
		interfaceAssertions(typ),
	)
	file := &ast.File{
		Name:  &ast.Ident{Name: packageName},
		Decls: decls,
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), file); err != nil {
		return nil, fmt.Errorf("format: %w", err)
	}

	out, err := imports.Process("generated.go", buf.Bytes(), &imports.Options{
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
		FormatOnly: false,
	})
	if err != nil {
		return nil, fmt.Errorf("goimports: %w\nsrc:\n%s", err, buf.String())
	}
	return out, nil
}
