package generate

import (
	"fmt"
	"go/ast"
	"go/parser"
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
	t, _, err := deriveWithRefs(name, obj, refMaps{}, Annotations{})
	return t, err
}

// deriveWithRefs is Derive plus a JSON-pointer → Go-identifier map
// used to resolve $ref expressions inside property / items /
// additionalProperties schemas. nil refs is equivalent to no
// $ref support. The returned siblings slice carries supplemental
// types the primary depends on (e.g. the typed object struct
// produced for a composite root with declared properties). The
// override Annotations are merged onto the schema's inline
// annotations with the override winning on every set field.
func deriveWithRefs(name string, obj *jsonschema.SchemaObject, refs refMaps, override Annotations) (Type, []Type, error) {
	annotations, err := ParseAnnotations(obj.Extra)
	if err != nil {
		return Type{}, nil, err
	}
	annotations = mergeAnnotations(annotations, override)
	if annotations.GoIdent != "" {
		name = annotations.GoIdent
	}

	doc := annotations.GoDoc
	if doc == "" {
		doc = obj.Description
	}

	t := Type{
		Name:              name,
		Doc:               doc,
		RejectUnknown:     rejectsAdditionalProperties(obj),
		DependentRequired: copyDependentRequired(obj.DependentRequired),
		AdditionalFields:  append([]GoAdditionalField(nil), annotations.GoAdditionalFields...),
	}

	// Composite root: type is a multi-element array of simple
	// kinds. Emit a discriminated-union struct. If the object
	// kind is one of the variants AND properties are declared,
	// derive a sibling typed-struct so the object branch is more
	// than map[string]any.
	if obj.Type != nil {
		if kinds, ok := obj.Type.TypeArray(); ok && len(kinds) > 1 {
			variants, err := deriveVariants(kinds, obj)
			if err != nil {
				return Type{}, nil, fmt.Errorf("composite root: %w", err)
			}
			var siblings []Type
			if hasObjectKind(kinds) && len(obj.Properties) > 0 {
				siblingName := name + "Object"
				branch := *obj
				branch.Type = nil
				branch.AllOf = nil
				siblingT, err := deriveStructShape(siblingName, &branch, refs, annotations)
				if err != nil {
					return Type{}, nil, fmt.Errorf("composite object branch: %w", err)
				}
				for i := range variants {
					if variants[i].Kind == "object" {
						variants[i].GoTypeExpr = ident(siblingName)
					}
				}
				siblings = append(siblings, siblingT)
			}
			t.Variants = variants
			return t, siblings, nil
		}
	}

	// Pure-$ref alias: the schema is just a {"$ref": ...} (with
	// at most descriptive metadata like default / description).
	// Emit `type Name <Target>` so the alias compiles to the same
	// underlying type as its target.
	if obj.Ref != "" && !hasStructuralContent(obj) {
		if name, ok := refs.byString[obj.Ref]; ok {
			t.Underlying = ident(name)
			return t, nil, nil
		}
	}

	// Scalar root: schema's type is a single primitive and there
	// are no properties. Emit `type Name <primitive>` with optional
	// constraint enforcement.
	if isScalarRoot(obj) {
		expr, err := derivePrimitive(obj)
		if err != nil {
			return Type{}, nil, fmt.Errorf("scalar root: %w", err)
		}
		t.Underlying = expr
		t.Constraints = deriveConstraints(obj)
		return t, nil, nil
	}

	// Array root: type=array with items schema. Emit
	// `type Name []ElemType`.
	if isArrayRoot(obj) {
		elem, err := derivePropertyType(obj.Items, refs)
		if err != nil {
			return Type{}, nil, fmt.Errorf("array root items: %w", err)
		}
		t.Underlying = &ast.ArrayType{Elt: elem}
		return t, nil, nil
	}

	// Map root: type=object with additionalProperties schema and no
	// declared properties. Emit `type Name map[K]V`.
	if isMapRoot(obj) {
		val, err := derivePropertyType(obj.AdditionalProperties, refs)
		if err != nil {
			return Type{}, nil, fmt.Errorf("map root value: %w", err)
		}
		key := mapKeyTypeFor(annotations)
		t.Underlying = &ast.MapType{Key: key, Value: val}
		return t, nil, nil
	}

	st, err := deriveStructShape(name, obj, refs, annotations)
	if err != nil {
		return Type{}, nil, err
	}
	st.Doc = doc
	if len(annotations.GoAdditionalFields) > 0 {
		st.AdditionalFields = append([]GoAdditionalField(nil), annotations.GoAdditionalFields...)
	}
	return st, nil, nil
}

// deriveStructShape produces the IR Type for a non-composite,
// non-scalar object schema. It reads the obj's properties / required
// / additionalProperties / dependentRequired and returns a struct
// Type. Used both for plain object roots and for the typed object
// branch of a composite root.
func deriveStructShape(name string, obj *jsonschema.SchemaObject, refs refMaps, parentAnnot Annotations) (Type, error) {
	annot, err := ParseAnnotations(obj.Extra)
	if err != nil {
		return Type{}, fmt.Errorf("annotations: %w", err)
	}

	t := Type{
		Name:              name,
		RejectUnknown:     rejectsAdditionalProperties(obj),
		DependentRequired: copyDependentRequired(obj.DependentRequired),
		AdditionalFields:  append([]GoAdditionalField(nil), annot.GoAdditionalFields...),
	}

	jsonNames := make([]string, 0, len(obj.Properties))
	for k := range obj.Properties {
		jsonNames = append(jsonNames, k)
	}
	sort.Strings(jsonNames)

	for _, jsonName := range jsonNames {
		propSchema := obj.Properties[jsonName]
		required := slices.Contains(obj.Required, jsonName)

		// Boolean schemas: `true` accepts anything → field type
		// `any`; `false` rejects everything, which we treat as
		// "do not emit a Go field" (the marshaler still
		// validates via RejectUnknownMembers if set).
		propObj, isObject := propSchema.TypeObject()
		if !isObject {
			b, isBool := propSchema.TypeBoolean()
			if !isBool {
				return Type{}, fmt.Errorf("property %q: schema is neither object nor boolean", jsonName)
			}
			if !b {
				continue
			}
			fieldType := ast.Expr(ident("any"))
			if !required {
				fieldType = &ast.StarExpr{X: fieldType}
			}
			t.Fields = append(t.Fields, Field{
				GoName:   exportedIdent(jsonName),
				JSONName: jsonName,
				TypeExpr: fieldType,
				Required: required,
			})
			continue
		}

		if isNullPropertySchema(&propObj) {
			t.NullProperties = append(t.NullProperties, NullProperty{
				JSONName: jsonName,
				Required: required,
			})
			continue
		}

		propAnnotations, err := ParseAnnotations(propObj.Extra)
		if err != nil {
			return Type{}, fmt.Errorf("property %q annotations: %w", jsonName, err)
		}
		if override, ok := parentAnnot.Fields[jsonName]; ok {
			propAnnotations = mergeAnnotations(propAnnotations, override)
		}

		var (
			fieldType   ast.Expr
			explicitGoType bool
		)
		if propAnnotations.GoType != "" {
			fieldType, err = parser.ParseExpr(propAnnotations.GoType)
			if err != nil {
				return Type{}, fmt.Errorf("property %q goType %q: %w", jsonName, propAnnotations.GoType, err)
			}
			explicitGoType = true
		} else {
			fieldType, err = derivePropertyType(propSchema, refs)
			if err != nil {
				return Type{}, fmt.Errorf("property %q: %w", jsonName, err)
			}
		}
		// goType wins as-is — the user has spelled the exact Go
		// type they want and the optional-pointer wrap would
		// silently double-indirect it.
		if !explicitGoType && !required && needsPointerForOptional(fieldType) {
			fieldType = &ast.StarExpr{X: fieldType}
		}
		goName := exportedIdent(jsonName)
		if propAnnotations.GoIdent != "" {
			goName = propAnnotations.GoIdent
		}
		t.Fields = append(t.Fields, Field{
			GoName:   goName,
			JSONName: jsonName,
			TypeExpr: fieldType,
			Required: required,
			JSONTags: propAnnotations.GoJSONTags,
		})
	}
	return t, nil
}

// hasStructuralContent reports whether obj declares its own type /
// properties / items / additionalProperties (i.e. anything that
// would shape the generated Go declaration), beyond a $ref and
// metadata like default / description / deprecated.
func hasStructuralContent(obj *jsonschema.SchemaObject) bool {
	if obj.Type != nil {
		return true
	}
	if len(obj.Properties) > 0 || len(obj.PatternProperties) > 0 {
		return true
	}
	if obj.AdditionalProperties != nil || obj.Items != nil {
		return true
	}
	if len(obj.PrefixItems) > 0 {
		return true
	}
	if len(obj.OneOf) > 0 || len(obj.AnyOf) > 0 {
		return true
	}
	return false
}

// hasObjectKind reports whether a composite type list contains the
// "object" simple type.
func hasObjectKind(kinds []jsonschema.SimpleType) bool {
	for _, k := range kinds {
		if string(k) == "object" {
			return true
		}
	}
	return false
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
// schema. It handles $ref (string-keyed and resolved-pointer-keyed),
// array-of-T, and primitive shapes.
func derivePropertyType(schema *jsonschema.Schema, refs refMaps) (ast.Expr, error) {
	obj, _ := schema.TypeObject()
	if obj.Ref != "" {
		if name, ok := refs.lookupRef(schema, obj.Ref); ok {
			return ident(name), nil
		}
		return ident("any"), nil
	}
	if obj.DynamicRef != "" {
		if name, ok := refs.lookupRef(schema, obj.DynamicRef); ok {
			return ident(name), nil
		}
		return ident("any"), nil
	}
	if obj.Type != nil {
		if s, ok := obj.Type.TypeString(); ok {
			switch s {
			case "array":
				if obj.Items == nil {
					return &ast.ArrayType{Elt: ident("any")}, nil
				}
				if _, ok := obj.Items.TypeObject(); !ok {
					// Boolean items schema (`true`/`false`) -> any.
					return &ast.ArrayType{Elt: ident("any")}, nil
				}
				elem, err := derivePropertyType(obj.Items, refs)
				if err != nil {
					return nil, fmt.Errorf("array items: %w", err)
				}
				return &ast.ArrayType{Elt: elem}, nil

			case "object":
				if len(obj.Properties) > 0 {
					return ident("any"), nil
				}
				if obj.AdditionalProperties == nil {
					return ident("any"), nil
				}
				if _, ok := obj.AdditionalProperties.TypeObject(); !ok {
					return &ast.MapType{Key: ident("string"), Value: ident("any")}, nil
				}
				val, err := derivePropertyType(obj.AdditionalProperties, refs)
				if err != nil {
					return nil, fmt.Errorf("additionalProperties: %w", err)
				}
				return &ast.MapType{Key: ident("string"), Value: val}, nil
			}
		}
	}
	if obj.Type == nil {
		// Schemas with applicator-only constraints (oneOf, anyOf,
		// allOf members already merged, etc.) and no declared type
		// are not yet structurally derivable; degrade to any so
		// the surrounding type still compiles.
		return ident("any"), nil
	}
	return derivePrimitive(&obj)
}

// refMaps bundles the two ways a $ref target can be located: by its
// raw $ref string and by the resolved *jsonschema.Schema pointer
// (the only stable identity for cross-document refs whose URL is
// not an in-scope key). The zero value is safe to pass.
type refMaps struct {
	byString  map[string]string
	byPointer map[*jsonschema.Schema]string
}

func (r refMaps) lookupRef(s *jsonschema.Schema, ref string) (string, bool) {
	if r.byString != nil {
		if name, ok := r.byString[ref]; ok {
			return name, true
		}
	}
	if r.byPointer != nil {
		if resolved := s.Resolved(); resolved != nil {
			if name, ok := r.byPointer[resolved]; ok {
				return name, true
			}
		}
		if name, ok := r.byPointer[s]; ok {
			return name, true
		}
	}
	return "", false
}

// copyDependentRequired clones the schema's dependentRequired map
// so later mutations do not bleed back into the parsed schema.
func copyDependentRequired(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
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
	b, ok := obj.AdditionalProperties.TypeBoolean()
	return ok && !b
}

func exportedIdent(jsonName string) string {
	var b strings.Builder
	upper := true
	for _, r := range jsonName {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if upper {
				b.WriteRune(unicode.ToUpper(r))
				upper = false
			} else {
				b.WriteRune(r)
			}
		default:
			// Skip punctuation/symbol runes (e.g. "$", "-", "_")
			// and uppercase the next letter so json names like
			// "$id" -> "Id" and "long-name" -> "LongName".
			upper = true
		}
	}
	return b.String()
}
