package generate

import (
	"fmt"

	"github.com/go-json-experiment/json"
)

// Overrides is the document-level go-codegen configuration loaded
// from a sidecar file via --overrides. It lets callers attach
// generator vocabulary to schemas they cannot edit (the published
// 2020-12 meta-schema being the primary motivating case).
type Overrides struct {
	// Refs is keyed by JSON pointer relative to the root schema:
	// "#" for the root, "#/$defs/foo" for a $defs entry, etc. Each
	// value's fields are merged into the matching schema's
	// Annotations before derivation, with the override winning on
	// any field collision.
	Refs map[string]Annotations `json:"refs,omitempty"`
}

// ParseOverrides decodes JSON-encoded sidecar overrides.
func ParseOverrides(buf []byte) (Overrides, error) {
	if len(buf) == 0 {
		return Overrides{}, nil
	}
	var o Overrides
	if err := json.Unmarshal(buf, &o); err != nil {
		return Overrides{}, fmt.Errorf("parse overrides: %w", err)
	}
	return o, nil
}

// merge folds src onto dst, with src winning on every set field.
// Nil-valued fields on src are ignored.
func mergeAnnotations(dst Annotations, src Annotations) Annotations {
	if src.GoIdent != "" {
		dst.GoIdent = src.GoIdent
	}
	if src.GoType != "" {
		dst.GoType = src.GoType
	}
	if len(src.GoImports) > 0 {
		dst.GoImports = append([]string(nil), src.GoImports...)
	}
	if src.GoDoc != "" {
		dst.GoDoc = src.GoDoc
	}
	if src.MapKeyType != "" {
		dst.MapKeyType = src.MapKeyType
	}
	if src.MapValueType != "" {
		dst.MapValueType = src.MapValueType
	}
	if len(src.GoJSONTags) > 0 {
		dst.GoJSONTags = append([]string(nil), src.GoJSONTags...)
	}
	if len(src.GoAdditionalFields) > 0 {
		dst.GoAdditionalFields = append([]GoAdditionalField(nil), src.GoAdditionalFields...)
	}
	if len(src.Fields) > 0 {
		if dst.Fields == nil {
			dst.Fields = make(map[string]Annotations, len(src.Fields))
		}
		for k, v := range src.Fields {
			dst.Fields[k] = mergeAnnotations(dst.Fields[k], v)
		}
	}
	return dst
}
