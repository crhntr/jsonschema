package jsonhandler

import (
	"bytes"
	_ "embed"
)

//go:embed jsonform.css
var stylesheet []byte

// Stylesheet returns jsonform.css, a starting stylesheet built on Open
// Props. The form does not need it; serve it, copy it, or write your own
// against the markup README.md documents.
func Stylesheet() []byte { return bytes.Clone(stylesheet) }

//go:embed schema.json
var metaSchema []byte

// MetaSchema returns a JSON Schema 2020-12 meta-schema that adds the x-form
// keyword. A Handler for it edits schemas, annotations included; the
// $ref to the 2020-12 meta-schema is built into [New].
func MetaSchema() []byte { return bytes.Clone(metaSchema) }
