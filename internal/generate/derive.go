package generate

import (
	"fmt"
	"go/ast"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/crhntr/jsonschema"
)

// Derive builds an IR Type for the given SchemaObject. name is the
// exported Go identifier the caller wants on the emitted declaration.
// Phase 3 only supports object schemas with primitive properties.
func Derive(name string, obj *jsonschema.SchemaObject) (Type, error) {
	annotations, err := ParseAnnotations(obj.Extra)
	if err != nil {
		return Type{}, err
	}
	if annotations.GoIdent != "" {
		name = annotations.GoIdent
	}

	doc := annotations.GoDoc
	if doc == "" {
		doc = obj.Description
	}

	t := Type{
		Name: name,
		Doc:  doc,
		Kind: KindStruct,
	}

	jsonNames := make([]string, 0, len(obj.Properties))
	for k := range obj.Properties {
		jsonNames = append(jsonNames, k)
	}
	sort.Strings(jsonNames)

	for _, jsonName := range jsonNames {
		propSchema := obj.Properties[jsonName]
		propObj, ok := propSchema.TypeObject()
		if !ok {
			return Type{}, fmt.Errorf("property %q: only object schemas are supported in Phase 3", jsonName)
		}
		fieldType, err := derivePrimitive(&propObj)
		if err != nil {
			return Type{}, fmt.Errorf("property %q: %w", jsonName, err)
		}
		t.Fields = append(t.Fields, Field{
			GoName:   exportedIdent(jsonName),
			JSONName: jsonName,
			TypeExpr: fieldType,
			Required: slices.Contains(obj.Required, jsonName),
		})
	}
	return t, nil
}

func derivePrimitive(obj *jsonschema.SchemaObject) (ast.Expr, error) {
	if obj.Type == nil {
		return nil, fmt.Errorf("missing type")
	}
	s, ok := obj.Type.TypeString()
	if !ok {
		return nil, fmt.Errorf("only single-type primitives supported in Phase 3")
	}
	switch s {
	case "string":
		return &ast.Ident{Name: "string"}, nil
	case "integer":
		return &ast.Ident{Name: "int"}, nil
	case "number":
		return &ast.Ident{Name: "float64"}, nil
	case "boolean":
		return &ast.Ident{Name: "bool"}, nil
	default:
		return nil, fmt.Errorf("type %q not supported in Phase 3", s)
	}
}

func exportedIdent(jsonName string) string {
	var b strings.Builder
	upper := true
	for _, r := range jsonName {
		switch {
		case r == '_' || r == '-' || r == ' ':
			upper = true
		case upper:
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
