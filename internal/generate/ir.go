package generate

import "go/ast"

// Type is the intermediate representation of a single named Go
// declaration emitted by the generator.
type Type struct {
	Name   string
	Doc    string
	Fields []Field
}

// Field is one field on the emitted Go struct.
type Field struct {
	GoName   string
	JSONName string
	TypeExpr ast.Expr
	Required bool
}
