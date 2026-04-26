package generate

import "go/ast"

// Type is the intermediate representation of a single named Go
// declaration emitted by the generator.
//
// Either Fields or Underlying is populated, never both. Fields
// describes a struct shape (object schema); Underlying describes a
// named alias of an existing Go type (scalar / slice / map schema).
type Type struct {
	Name string
	Doc  string

	// Underlying is set for scalar/alias declarations. Mutually
	// exclusive with Fields.
	Underlying ast.Expr

	// Fields is set for struct declarations.
	Fields []Field

	// NullProperties names JSON members whose schema is
	// {"type":"null"}. These have no Go field — null carries no
	// useful Go state — but the generated marshaler always writes
	// them as null and the unmarshaler verifies they were received
	// as null (and, if Required, present).
	NullProperties []NullProperty

	// RejectUnknown is set when the source schema declares
	// additionalProperties: false; the generated UnmarshalJSONFrom
	// then refuses unknown members.
	RejectUnknown bool

	// Constraints carries assertion keywords from the schema that
	// the generated UnmarshalJSONFrom must enforce.
	Constraints Constraints

	// Variants is set when the schema declares a multi-type
	// composite (e.g. type:["string","boolean"]). Each variant
	// represents one allowed JSON shape. Mutually exclusive with
	// Underlying and Fields.
	Variants []Variant

	// DependentRequired captures the JSON Schema dependentRequired
	// applicator: when key X is present in the JSON, every
	// property listed in DependentRequired[X] must also be
	// present. The generated UnmarshalJSONFrom rejects payloads
	// that violate any dependency.
	DependentRequired map[string][]string
}

// Variant is one branch of a composite type. The generated Go shape
// is a discriminated struct holding one boolean is<Kind> flag plus
// one value field for each variant.
type Variant struct {
	// Kind is the JSON Schema simple-type label
	// ("string", "integer", "number", "boolean", "null",
	// "array", "object").
	Kind string

	// GoTypeExpr is the Go type expression carrying the value when
	// this variant is active. Nil for the "null" variant (which is
	// represented by a presence-only flag).
	GoTypeExpr ast.Expr
}

// Constraints holds the JSON Schema assertion keywords that the
// generated marshaler validates after decoding. Numeric bounds are
// stored as their raw JSON text so they splice losslessly into Go
// numeric literals regardless of int / float representation.
type Constraints struct {
	MinLength *int
	MaxLength *int
	Minimum   *string
	Maximum   *string
	// Enum is the raw JSON text of each permitted value, in
	// declaration order. The generated UnmarshalJSONFrom compares
	// the decoded scalar against each entry.
	Enum []string
	// Pattern is the ECMA-262 regular expression a string scalar
	// must match. Compiled once at package init via regexp.MustCompile.
	Pattern string
}

// Field is one field on the emitted Go struct.
type Field struct {
	GoName   string
	JSONName string
	TypeExpr ast.Expr
	Required bool
	// JSONTags are additional jsonv2 struct-tag flags
	// (e.g. "omitempty", "format:RFC3339Nano") spliced into the
	// emitted json tag verbatim, after the json name.
	JSONTags []string
}

// NullProperty is a JSON member whose schema fixes its value to
// null. The generated marshaler emits it unconditionally; the
// unmarshaler enforces the null literal and (if Required) presence.
// It does not contribute a Go struct field.
type NullProperty struct {
	JSONName string
	Required bool
}
