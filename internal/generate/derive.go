package generate

import (
	"fmt"
	"go/ast"
	"slices"
	"sort"
	"strconv"
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
		Name:          name,
		Doc:           doc,
		RejectUnknown: rejectsAdditionalProperties(obj),
	}

	// Scalar root: schema's type is a single primitive and there
	// are no properties. Emit `type Name <primitive>` with optional
	// constraint enforcement.
	if isScalarRoot(obj) {
		expr, err := derivePrimitive(obj)
		if err != nil {
			return Type{}, fmt.Errorf("scalar root: %w", err)
		}
		t.Underlying = expr
		t.Constraints = deriveConstraints(obj)
		return t, nil
	}

	// Array root: type=array with items schema. Emit
	// `type Name []ElemType`.
	if isArrayRoot(obj) {
		elemObj, ok := obj.Items.TypeObject()
		if !ok {
			return Type{}, fmt.Errorf("array root: items must be an object schema")
		}
		elem, err := derivePrimitive(&elemObj)
		if err != nil {
			return Type{}, fmt.Errorf("array root items: %w", err)
		}
		t.Underlying = &ast.ArrayType{Elt: elem}
		return t, nil
	}

	// Map root: type=object with additionalProperties schema and no
	// declared properties. Emit `type Name map[K]V`.
	if isMapRoot(obj) {
		valObj, ok := obj.AdditionalProperties.TypeObject()
		if !ok {
			return Type{}, fmt.Errorf("map root: additionalProperties must be an object schema")
		}
		val, err := derivePrimitive(&valObj)
		if err != nil {
			return Type{}, fmt.Errorf("map root value: %w", err)
		}
		key := mapKeyTypeFor(annotations)
		t.Underlying = &ast.MapType{Key: key, Value: val}
		return t, nil
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
		required := slices.Contains(obj.Required, jsonName)
		if !required {
			fieldType = &ast.StarExpr{X: fieldType}
		}
		t.Fields = append(t.Fields, Field{
			GoName:   exportedIdent(jsonName),
			JSONName: jsonName,
			TypeExpr: fieldType,
			Required: required,
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

// isScalarRoot reports whether obj describes a single primitive
// (string / integer / number / boolean) with no object/array shape.
func isScalarRoot(obj *jsonschema.SchemaObject) bool {
	if len(obj.Properties) > 0 {
		return false
	}
	if obj.Type == nil {
		return false
	}
	s, ok := obj.Type.TypeString()
	if !ok {
		return false
	}
	switch s {
	case "string", "integer", "number", "boolean":
		return true
	default:
		return false
	}
}

// deriveConstraints lifts the assertion keywords supported in
// Phase 6 off obj into an IR Constraints value.
func deriveConstraints(obj *jsonschema.SchemaObject) Constraints {
	var c Constraints
	if n, ok := jsontextInt(obj.MinLength); ok {
		c.MinLength = &n
	}
	if n, ok := jsontextInt(obj.MaxLength); ok {
		c.MaxLength = &n
	}
	if s, ok := jsontextNumber(obj.Minimum); ok {
		c.Minimum = &s
	}
	if s, ok := jsontextNumber(obj.Maximum); ok {
		c.Maximum = &s
	}
	for _, e := range obj.Enum {
		c.Enum = append(c.Enum, string(e))
	}
	c.Pattern = obj.Pattern
	return c
}

// jsontextNumber returns the raw JSON number text of v.
func jsontextNumber(v []byte) (string, bool) {
	if len(v) == 0 {
		return "", false
	}
	return string(v), true
}

// jsontextInt parses a jsontext.Value holding a JSON integer.
func jsontextInt(v []byte) (int, bool) {
	if len(v) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(string(v))
	if err != nil {
		return 0, false
	}
	return n, true
}

// isArrayRoot reports whether obj describes a homogeneous array.
func isArrayRoot(obj *jsonschema.SchemaObject) bool {
	if obj.Items == nil {
		return false
	}
	if obj.Type == nil {
		return false
	}
	s, ok := obj.Type.TypeString()
	return ok && s == "array"
}

// isMapRoot reports whether obj describes a string-keyed map (an
// object schema with an additionalProperties subschema and no
// declared properties).
func isMapRoot(obj *jsonschema.SchemaObject) bool {
	if obj.AdditionalProperties == nil || len(obj.Properties) > 0 {
		return false
	}
	if obj.Type != nil {
		s, ok := obj.Type.TypeString()
		if ok && s != "object" {
			return false
		}
	}
	if _, ok := obj.AdditionalProperties.TypeObject(); !ok {
		return false
	}
	return true
}

// mapKeyTypeFor returns the AST identifier for the map key type
// declared on the schema's go-codegen vocabulary, defaulting to
// `string`.
func mapKeyTypeFor(a Annotations) ast.Expr {
	if a.MapKeyType != "" {
		return ident(a.MapKeyType)
	}
	return ident("string")
}

// rejectsAdditionalProperties reports whether obj declares
// additionalProperties: false (i.e. the boolean schema `false`).
func rejectsAdditionalProperties(obj *jsonschema.SchemaObject) bool {
	if obj.AdditionalProperties == nil {
		return false
	}
	b, ok := obj.AdditionalProperties.TypeBool()
	return ok && !b
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
