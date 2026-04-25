package generate

import "go/ast"

// Kind identifies the Go shape of an IR Type.
type Kind int

const (
	// KindStruct is a Go struct generated from an object schema.
	KindStruct Kind = iota + 1
)

// Type is the intermediate representation of a single named Go
// declaration emitted by the generator.
type Type struct {
	Name   string
	Doc    string
	Kind   Kind
	Fields []Field
}

// Field is one field inside a Type whose Kind is KindStruct.
type Field struct {
	GoName   string
	JSONName string
	TypeExpr ast.Expr
	Required bool
}
