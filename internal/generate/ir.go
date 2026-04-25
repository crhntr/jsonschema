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

	// RejectUnknown is set when the source schema declares
	// additionalProperties: false; the generated UnmarshalJSONFrom
	// then refuses unknown members.
	RejectUnknown bool

	// Constraints carries assertion keywords from the schema that
	// the generated UnmarshalJSONFrom must enforce.
	Constraints Constraints
}

// Constraints holds the JSON Schema assertion keywords that the
// generated marshaler validates after decoding.
type Constraints struct {
	MinLength *int
	MaxLength *int
}

// Field is one field on the emitted Go struct.
type Field struct {
	GoName   string
	JSONName string
	TypeExpr ast.Expr
	Required bool
}
