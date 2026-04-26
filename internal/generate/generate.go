package generate

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"sort"
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

// Generate emits a complete Go source file for the given schema. It
// produces one Go type per $defs entry plus the root type itself,
// resolving $ref links between them. The result is run through
// goimports.
func Generate(schema *jsonschema.SchemaObject, typeName, packageName string) ([]byte, error) {
	refs, defNames, err := buildRefMap(schema, typeName)
	if err != nil {
		return nil, err
	}

	rootT, err := deriveWithRefs(typeName, schema, refs)
	if err != nil {
		return nil, fmt.Errorf("derive %s: %w", typeName, err)
	}
	types := []Type{rootT}
	for _, key := range defNames {
		defObj, ok := schema.Defs[key].TypeObject()
		if !ok {
			return nil, fmt.Errorf("$defs/%s is not an object schema", key)
		}
		defT, err := deriveWithRefs(refs["#/$defs/"+key], &defObj, refs)
		if err != nil {
			return nil, fmt.Errorf("derive $defs/%s: %w", key, err)
		}
		types = append(types, defT)
	}

	decls := []ast.Decl{
		importDecl(
			"encoding/json/v2",
			"encoding/json/jsontext",
			"fmt",
			"regexp",
		),
	}
	for _, t := range types {
		td, err := emitTypeDecls(t)
		if err != nil {
			return nil, err
		}
		decls = append(decls, td...)
	}
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

// emitTypeDecls returns all the declarations (type + pattern var +
// methods + interface asserts) for a single IR Type.
func emitTypeDecls(t Type) ([]ast.Decl, error) {
	decls := []ast.Decl{Emit(t)}
	if pat := EmitPatternVar(t); pat != nil {
		decls = append(decls, pat)
	}
	if len(t.Variants) > 0 {
		decls = append(decls, emitCompositeAccessors(t)...)
		decls = append(decls, emitCompositeMarshal(t))
		um, err := emitCompositeUnmarshal(t)
		if err != nil {
			return nil, err
		}
		decls = append(decls, um)
	} else {
		decls = append(decls, EmitMarshal(t), EmitUnmarshal(t))
	}
	decls = append(decls, interfaceAssertions(t))
	return decls, nil
}

// buildRefMap returns a JSON-pointer → Go-identifier map for the
// root schema and every $defs entry, plus the sorted list of $defs
// keys so callers can iterate deterministically. goIdent annotations
// on individual $defs entries override the default name.
func buildRefMap(schema *jsonschema.SchemaObject, rootName string) (map[string]string, []string, error) {
	refs := map[string]string{
		"#": rootName,
	}
	keys := make([]string, 0, len(schema.Defs))
	for k := range schema.Defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		defObj, ok := schema.Defs[k].TypeObject()
		if !ok {
			return nil, nil, fmt.Errorf("$defs/%s is not an object schema", k)
		}
		annot, err := ParseAnnotations(defObj.Extra)
		if err != nil {
			return nil, nil, fmt.Errorf("$defs/%s annotations: %w", k, err)
		}
		name := k
		if annot.GoIdent != "" {
			name = annot.GoIdent
		}
		refs["#/$defs/"+k] = name
	}
	return refs, keys, nil
}
