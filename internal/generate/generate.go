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
//
// Generate accepts a raw SchemaObject. Callers with a fully
// resolved *jsonschema.Schema (refs and $dynamicRefs already
// attached) should prefer GenerateFromSchema so allOf members that
// are $refs get followed.
func Generate(schema *jsonschema.SchemaObject, typeName, packageName string) ([]byte, error) {
	flat, err := flatten(*schema)
	if err != nil {
		return nil, err
	}
	return generateFromObject(flat, typeName, packageName, Overrides{})
}

// GenerateFromSchema is the resolved-input entry point. The schema
// must already be Resolve()'d so $ref / $dynamicRef targets are
// reachable through *jsonschema.Schema.Resolved().
func GenerateFromSchema(s *jsonschema.Schema, typeName, packageName string, overrides Overrides) ([]byte, error) {
	target := s
	if r := s.Resolved(); r != nil {
		target = r
	}
	obj, ok := target.TypeObject()
	if !ok {
		return nil, fmt.Errorf("root schema must be an object schema")
	}
	flat, err := flatten(obj)
	if err != nil {
		return nil, err
	}
	return generateFromObject(flat, typeName, packageName, overrides)
}

func generateFromObject(schema jsonschema.SchemaObject, typeName, packageName string, overrides Overrides) ([]byte, error) {
	refs, defNames, err := buildRefMap(&schema, typeName)
	if err != nil {
		return nil, err
	}

	rootT, rootSiblings, err := deriveWithRefs(typeName, &schema, refs, overrides.Refs["#"])
	if err != nil {
		return nil, fmt.Errorf("derive %s: %w", typeName, err)
	}
	types := append([]Type{rootT}, rootSiblings...)
	for _, key := range defNames {
		defObj, ok := schema.Defs[key].TypeObject()
		if !ok {
			return nil, fmt.Errorf("$defs/%s is not an object schema", key)
		}
		defT, defSiblings, err := deriveWithRefs(refs.byString["#/$defs/"+key], &defObj, refs, overrides.Refs["#/$defs/"+key])
		if err != nil {
			return nil, fmt.Errorf("derive $defs/%s: %w", key, err)
		}
		types = append(types, defT)
		types = append(types, defSiblings...)
	}

	jsonPath, jsontextPath := overrides.jsonImports()
	decls := []ast.Decl{
		importDecl(jsonPath, jsontextPath, "fmt", "regexp"),
	}
	for _, t := range types {
		td, err := emitTypeDecls(t)
		if err != nil {
			return nil, fmt.Errorf("emit %s: %w", t.Name, err)
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

// buildRefMap returns a refMaps with both the JSON-pointer and the
// resolved-*Schema lookups for the root schema and every $defs
// entry, plus the sorted list of $defs keys so callers can iterate
// deterministically. goIdent annotations on individual $defs
// entries override the default name.
func buildRefMap(schema *jsonschema.SchemaObject, rootName string) (refMaps, []string, error) {
	refs := refMaps{
		byString:  map[string]string{"#": rootName},
		byPointer: map[*jsonschema.Schema]string{},
	}
	keys := make([]string, 0, len(schema.Defs))
	for k := range schema.Defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		defSchema := schema.Defs[k]
		defObj, ok := defSchema.TypeObject()
		if !ok {
			return refMaps{}, nil, fmt.Errorf("$defs/%s is not an object schema", k)
		}
		annot, err := ParseAnnotations(defObj.Extra)
		if err != nil {
			return refMaps{}, nil, fmt.Errorf("$defs/%s annotations: %w", k, err)
		}
		name := k
		if annot.GoIdent != "" {
			name = annot.GoIdent
		}
		refs.byString["#/$defs/"+k] = name
		refs.byPointer[defSchema] = name
		if r := defSchema.Resolved(); r != nil {
			refs.byPointer[r] = name
		}
	}
	return refs, keys, nil
}
