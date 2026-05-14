package generate

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestFlatten_DuplicateSiblingRef(t *testing.T) {
	// Two allOf members both point at the same target schema. allOf
	// is conjunctive and idempotent — re-merging the same target is
	// not a cycle, so flatten must accept this shape.
	shared := mustParseSchema(t, `{"type": "object", "properties": {"a": {"type": "string"}}}`)
	parent := jsonschema.SchemaObject{
		AllOf: []*jsonschema.Schema{shared, shared},
	}
	if _, err := flatten(parent); err != nil {
		t.Fatalf("flatten: duplicate sibling $ref should not be flagged as cycle: %v", err)
	}
}

func mustParseSchema(t *testing.T, src string) *jsonschema.Schema {
	t.Helper()
	s, err := jsonschema.Parse(jsontext.Value(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return s
}
