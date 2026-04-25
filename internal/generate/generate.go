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

	file := &ast.File{
		Name: &ast.Ident{Name: packageName},
		Decls: []ast.Decl{
			importDecl(
				"encoding/json/v2",
				"encoding/json/jsontext",
				"fmt",
			),
			Emit(typ),
			EmitMarshal(typ),
			EmitUnmarshal(typ),
		},
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
