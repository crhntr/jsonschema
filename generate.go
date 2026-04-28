package jsonschema

import "os"

// ToGo is a placeholder for the schema-to-Go-code generator that is
// being developed on a separate branch. The implementation in this
// package is a no-op; callers should not rely on it. It exists here
// so the codegen branch can land without churning the public API.
func ToGo(root *os.Root, m *SchemaObject) error {
	return nil
}
