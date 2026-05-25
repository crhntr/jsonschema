// Package generate emits Go types from JSON Schema 2020-12 documents.
package generate

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
)

// VocabURI identifies the Go-codegen vocabulary keywords are siblings of.
const VocabURI = "https://crhntr.github.io/jsonschema/vocab/go-codegen"

// Annotations holds the decoded values of go-codegen vocabulary
// keywords on a single subschema. Zero values mean the keyword was
// absent. The json tags are used both when an Annotations is parsed
// from a sidecar overrides file and (via reflect-based field name
// matching in ParseAnnotations) when individual keys appear in a
// SchemaObject's Extra map.
type Annotations struct {
	// GoIdent overrides the exported Go identifier for the type or
	// field generated from this subschema.
	GoIdent string `json:"goIdent,omitempty"`
	// GoType is an explicit Go type expression that wins over a
	// derived type. Identifiers must resolve through GoImports.
	GoType string `json:"goType,omitempty"`
	// GoImports lists package paths whose identifiers may appear in
	// GoType, MapKeyType, or MapValueType.
	GoImports []string `json:"goImports,omitempty"`
	// GoDoc is the doc comment to attach to the generated declaration.
	// Falls back to the schema's `description` when empty.
	GoDoc string `json:"goDoc,omitempty"`
	// MapKeyType is the Go type for object map keys when the schema
	// generates a map.
	MapKeyType string `json:"mapKeyType,omitempty"`
	// MapValueType is the Go type for object map values when the
	// schema generates a map.
	MapValueType string `json:"mapValueType,omitempty"`
	// GoJSONTags lists extra struct-tag flags to splice into the
	// json:"…" tag verbatim after the json name.
	GoJSONTags []string `json:"goJSONTags,omitempty"`
	// GoAdditionalFields declares Go struct fields the generator
	// should append to the emitted struct in addition to those
	// derived from JSON properties. Useful for resolution
	// metadata or other non-wire state that the hand-rolled type
	// carries alongside the JSON-derived shape.
	//
	// The list ordering is preserved on output. An entry whose
	// GoIdent is empty is emitted as an embedded field.
	GoAdditionalFields []GoAdditionalField `json:"goAdditionalFields,omitempty"`
	// Fields carries per-property annotations keyed by JSON
	// property name. Each entry's annotations override (with merge
	// semantics) the inline annotations on the property's own
	// schema. Used in sidecar overrides files when the author
	// can't edit the underlying schema, e.g. to retype a single
	// field on the merged 2020-12 meta-schema's SchemaObject.
	Fields map[string]Annotations `json:"fields,omitempty"`
}

// GoAdditionalField describes one Go struct field declared via the
// goAdditionalFields vocabulary entry. Fields with an empty GoIdent
// list are emitted as embedded; multiple identifiers declare
// multiple fields sharing the same type, mirroring Go's
// `a, b, c T` syntax.
type GoAdditionalField struct {
	GoIdent stringList `json:"goIdent,omitempty"`
	GoType  string     `json:"goType"`
	GoTag   string     `json:"goTag,omitempty"`
	GoDoc   string     `json:"goDoc,omitempty"`
}

// stringList accepts either a single JSON string or a JSON array of
// strings. The single-string form folds to a one-element list so
// downstream code can treat both shapes uniformly.
type stringList []string

func (s *stringList) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case jsontext.KindString:
		var v string
		if err := json.UnmarshalDecode(dec, &v); err != nil {
			return err
		}
		*s = []string{v}
		return nil
	case jsontext.KindBeginArray:
		type alias []string
		var v alias
		if err := json.UnmarshalDecode(dec, &v); err != nil {
			return err
		}
		*s = stringList(v)
		return nil
	default:
		return errors.New("goIdent: expected string or array of strings")
	}
}

func (s stringList) MarshalJSONTo(enc *jsontext.Encoder) error {
	if len(s) == 1 {
		return json.MarshalEncode(enc, s[0])
	}
	return json.MarshalEncode(enc, []string(s))
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
		{"goAdditionalFields", &a.GoAdditionalFields},
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
