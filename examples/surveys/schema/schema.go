// Package schema holds the JSON Schema for survey definitions: what an
// author edits. The schema for a survey's answers is built from a
// definition (see package survey).
package schema

import _ "embed"

//go:embed definition.json
var definition []byte

// Definition returns the schema document for a survey definition.
func Definition() []byte { return definition }
