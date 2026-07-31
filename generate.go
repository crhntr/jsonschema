package jsonschema

import "os"

// ToGo is a placeholder for a library entry point to the
// schema-to-Go-code generator. The generator currently lives in
// internal/generate and is exposed through the jsch CLI; this
// implementation is a no-op and callers should not rely on it.
func ToGo(root *os.Root, m *SchemaObject) error {
	return nil
}
