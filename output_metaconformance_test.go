package jsonschema_test

import (
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/crhntr/jsonschema"
)

// TestOutputJSONIsSpecConformant feeds Output documents emitted by
// this library back through the same library, validating them against
// the JSON Schema 2020-12 output schema fixture at
// testdata/schema/json-schema.org/draft/2020-12/output/schema.json.
//
// The output schema's two conditional rules — error-or-errors-required
// when valid:false, and absoluteKeywordLocation-required when
// keywordLocation crosses $ref / $dynamicRef — are also enforced by
// MarshalJSONTo, so a failure here indicates either the marshaler is
// permitting something it shouldn't or the spec schema disagrees with
// our interpretation.
func TestOutputJSONIsSpecConformant(t *testing.T) {
	srv, client := startSchemaServer(t)
	_ = srv

	r := &jsonschema.Resolver{Client: client}
	outSchema, err := r.Resolve(t.Context(), "https://json-schema.org/draft/2020-12/output/schema")
	if err != nil {
		t.Fatalf("load output schema: %v", err)
	}

	// Build a small Validate run that exercises a representative slice
	// of keywords (type, properties, allOf, $ref) and produces both
	// success and failure results.
	if _, err := os.Stat("testdata/schema/json-schema.org/draft/2020-12/output/schema.json"); err != nil {
		t.Fatalf("output schema fixture missing: %v", err)
	}

	// Use a parallel resolver to load a sample schema with a $ref so
	// the Output we produce includes absoluteKeywordLocation.
	rSample := &jsonschema.Resolver{Client: client}
	if _, err := rSample.Load("https://example.com/mc/sample", []byte(`{
		"$id": "https://example.com/mc/sample",
		"$defs": {
			"pos": { "type": "integer", "minimum": 1 }
		},
		"properties": {
			"n": { "$ref": "#/$defs/pos" }
		}
	}`)); err != nil {
		t.Fatalf("load sample: %v", err)
	}
	sample, err := rSample.Resolve(t.Context(), "https://example.com/mc/sample")
	if err != nil {
		t.Fatalf("resolve sample: %v", err)
	}

	cases := []struct {
		name     string
		instance []byte
	}{
		{"valid instance", []byte(`{"n":5}`)},
		{"invalid instance (ref-target fails)", []byte(`{"n":0}`)},
		{"invalid instance (type)", []byte(`{"n":"x"}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sample.Validate("inst", tc.instance)

			for _, fmtName := range []string{"flag", "basic", "detailed", "verbose"} {
				t.Run(fmtName, func(t *testing.T) {
					var picked jsonschema.Output
					switch fmtName {
					case "flag":
						picked = out.Flag()
					case "basic":
						picked = out.Basic()
					case "detailed":
						picked = out.Detailed()
					case "verbose":
						picked = out.Verbose()
					}
					emitted, err := json.Marshal(picked)
					if err != nil {
						t.Fatalf("Marshal(%s): %v", fmtName, err)
					}
					meta := outSchema.Validate("emitted/"+fmtName, emitted)
					if !meta.Valid {
						t.Errorf("%s output failed meta-conformance against the 2020-12 output schema:\n  emitted: %s\n  reason: %v",
							fmtName, emitted, meta.AsError())
					}
				})
			}
		})
	}
}
