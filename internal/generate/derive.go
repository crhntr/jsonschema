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
func Derive(name string, obj *jsonschema.SchemaObject) (Type, error) {
	return deriveWithRefs(name, obj, nil)
}

// deriveWithRefs is Derive plus a JSON-pointer → Go-identifier map
// used to resolve $ref expressions inside property / items /
// additionalProperties schemas. nil refs is equivalent to no
// $ref support.
func deriveWithRefs(name string, obj *jsonschema.SchemaObject, refs map[string]string) (Type, error) {
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

	// Composite root: type is a multi-element array of simple
	// kinds. Emit a discriminated-union struct.
	if obj.Type != nil {
		if kinds, ok := obj.Type.TypeArray(); ok && len(kinds) > 1 {
			variants, err := deriveVariants(kinds, obj)
			if err != nil {
				return Type{}, fmt.Errorf("composite root: %w", err)
			}
			t.Variants = variants
			return t, nil
		}
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
		required := slices.Contains(obj.Required, jsonName)

		// {"type":"null"} carries no useful Go state — record it as
		// a NullProperty for the marshaler/unmarshaler to enforce on
		// the wire.
		if isNullPropertySchema(&propObj) {
			t.NullProperties = append(t.NullProperties, NullProperty{
				JSONName: jsonName,
				Required: required,
			})
			continue
		}

		fieldType, err := derivePropertyType(&propObj, refs)
		if err != nil {
			return Type{}, fmt.Errorf("property %q: %w", jsonName, err)
		}
		if !required && needsPointerForOptional(fieldType) {
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

// deriveVariants turns a JSON Schema type array (e.g.
// ["string","integer"]) into the IR Variant slice. Each kind picks
// up its Go representation; for "array" and "object" the items /
// properties on obj inform the chosen Go type.
func deriveVariants(kinds []jsonschema.SimpleType, obj *jsonschema.SchemaObject) ([]Variant, error) {
	out := make([]Variant, 0, len(kinds))
	for _, k := range kinds {
		s := string(k)
		v := Variant{Kind: s}
		switch s {
		case "string":
			v.GoTypeExpr = ident("string")
		case "integer":
			v.GoTypeExpr = ident("int")
		case "number":
			v.GoTypeExpr = ident("float64")
		case "boolean":
			v.GoTypeExpr = ident("bool")
		case "null":
			v.GoTypeExpr = nil
		case "array":
			elem, err := deriveItemsType(obj)
			if err != nil {
				return nil, fmt.Errorf("array variant: %w", err)
			}
			v.GoTypeExpr = &ast.ArrayType{Elt: elem}
		case "object":
			val, err := deriveAdditionalPropertiesType(obj)
			if err != nil {
				return nil, fmt.Errorf("object variant: %w", err)
			}
			v.GoTypeExpr = &ast.MapType{Key: ident("string"), Value: val}
		default:
			return nil, fmt.Errorf("unknown simple type %q", s)
		}
		out = append(out, v)
	}
	return out, nil
}

// deriveItemsType returns the Go type expression for obj.Items, or
// `any` when items is missing or not a primitive object schema.
func deriveItemsType(obj *jsonschema.SchemaObject) (ast.Expr, error) {
	if obj.Items == nil {
		return ident("any"), nil
	}
	itemObj, ok := obj.Items.TypeObject()
	if !ok {
		return ident("any"), nil
	}
	return derivePrimitive(&itemObj)
}

// deriveAdditionalPropertiesType returns the Go type expression for
// obj.AdditionalProperties, or `any` when missing / non-primitive.
func deriveAdditionalPropertiesType(obj *jsonschema.SchemaObject) (ast.Expr, error) {
	if obj.AdditionalProperties == nil {
		return ident("any"), nil
	}
	apObj, ok := obj.AdditionalProperties.TypeObject()
	if !ok {
		return ident("any"), nil
	}
	return derivePrimitive(&apObj)
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

// needsPointerForOptional reports whether an optional field should
// be wrapped in a pointer to distinguish absent from the zero value.
// Slices and maps have a natural nil zero value, so the wrap would
// be redundant.
func needsPointerForOptional(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.ArrayType, *ast.MapType:
		return false
	}
	return true
}

// derivePropertyType picks the Go type expression for a property
// schema. It handles $ref, array-of-T, and primitive shapes. Map and
// inline-object property shapes will land in later phases.
func derivePropertyType(obj *jsonschema.SchemaObject, refs map[string]string) (ast.Expr, error) {
	if obj.Ref != "" {
		if refs == nil {
			return nil, fmt.Errorf("$ref %q encountered without a ref map", obj.Ref)
		}
		name, ok := refs[obj.Ref]
		if !ok {
			return nil, fmt.Errorf("$ref %q has no resolved Go type", obj.Ref)
		}
		return ident(name), nil
	}
	if obj.Type != nil {
		if s, ok := obj.Type.TypeString(); ok && s == "array" && obj.Items != nil {
			itemObj, ok := obj.Items.TypeObject()
			if !ok {
				return nil, fmt.Errorf("array items must be an object schema")
			}
			elem, err := derivePropertyType(&itemObj, refs)
			if err != nil {
				return nil, fmt.Errorf("array items: %w", err)
			}
			return &ast.ArrayType{Elt: elem}, nil
		}
	}
	return derivePrimitive(obj)
}

// isNullPropertySchema reports whether obj is the singleton schema
// `{"type":"null"}`.
func isNullPropertySchema(obj *jsonschema.SchemaObject) bool {
	if obj.Type == nil {
		return false
	}
	s, ok := obj.Type.TypeString()
	return ok && s == "null"
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
