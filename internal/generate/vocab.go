// Package generate emits Go types from JSON Schema 2020-12 documents.
package generate

import (
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// VocabURI identifies the Go-codegen vocabulary keywords are siblings of.
const VocabURI = "https://crhntr.github.io/jsonschema/vocab/go-codegen"

// Annotations holds the decoded values of go-codegen vocabulary
// keywords on a single subschema. Zero values mean the keyword was
// absent.
type Annotations struct {
	// GoIdent overrides the exported Go identifier for the type or
	// field generated from this subschema.
	GoIdent string
	// GoType is an explicit Go type expression that wins over a
	// derived type. Identifiers must resolve through GoImports.
	GoType string
	// GoImports lists package paths whose identifiers may appear in
	// GoType, MapKeyType, or MapValueType.
	GoImports []string
	// GoDoc is the doc comment to attach to the generated declaration.
	// Falls back to the schema's `description` when empty.
	GoDoc string
	// MapKeyType is the Go type for object map keys when the schema
	// generates a map.
	MapKeyType string
	// MapValueType is the Go type for object map values when the
	// schema generates a map.
	MapValueType string
	// GoJSONTags lists extra struct-tag flags to splice into the
	// json:"…" tag verbatim after the json name.
	GoJSONTags []string
}

// ParseAnnotations decodes the go-codegen vocabulary entries from a
// SchemaObject.Extra map. Keywords not in the vocabulary are ignored.
func ParseAnnotations(extra map[string]jsontext.Value) (Annotations, error) {
	var a Annotations
	for _, e := range []struct {
		key string
		dst any
	}{
		{"goIdent", &a.GoIdent},
		{"goType", &a.GoType},
		{"goDoc", &a.GoDoc},
		{"mapKeyType", &a.MapKeyType},
		{"mapValueType", &a.MapValueType},
		{"goImports", &a.GoImports},
		{"goJSONTags", &a.GoJSONTags},
	} {
		raw, ok := extra[e.key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, e.dst); err != nil {
			return Annotations{}, fmt.Errorf("decode %s: %w", e.key, err)
		}
	}
	return a, nil
}
