package jsonschema_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

func TestOutputMarshalRequiredFields(t *testing.T) {
	o := jsonschema.Output{
		Valid:            true,
		KeywordLocation:  "",
		InstanceLocation: "",
	}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"valid":true,"keywordLocation":"","instanceLocation":""}`
	if eq, err := jsonschema.Equal(got, []byte(want)); err != nil || !eq {
		t.Fatalf("Marshal: got %s, want %s", got, want)
	}
}

func TestOutputMarshalEmitsErrorOnFailure(t *testing.T) {
	o := jsonschema.Output{
		Valid:            false,
		KeywordLocation:  "/type",
		InstanceLocation: "",
		Error:            "got string, want number",
	}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"valid":false,"keywordLocation":"/type","instanceLocation":"","error":"got string, want number"}`
	if eq, err := jsonschema.Equal(got, []byte(want)); err != nil || !eq {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestOutputMarshalErrorsArrayInsteadOfMessage(t *testing.T) {
	o := jsonschema.Output{
		Valid:            false,
		KeywordLocation:  "",
		InstanceLocation: "",
		Errors: []jsonschema.Output{
			{Valid: false, KeywordLocation: "/type", InstanceLocation: "", Error: "bad type"},
			{Valid: false, KeywordLocation: "/minItems", InstanceLocation: "", Error: "too few items"},
		},
	}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{
		"valid": false,
		"keywordLocation": "",
		"instanceLocation": "",
		"errors": [
			{"valid": false, "keywordLocation": "/type", "instanceLocation": "", "error": "bad type"},
			{"valid": false, "keywordLocation": "/minItems", "instanceLocation": "", "error": "too few items"}
		]
	}`
	if eq, err := jsonschema.Equal(got, []byte(want)); err != nil || !eq {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestOutputMarshalRejectsInvalidWithoutErrorOrErrors(t *testing.T) {
	o := jsonschema.Output{
		Valid:            false,
		KeywordLocation:  "/type",
		InstanceLocation: "",
	}
	if _, err := json.Marshal(o); err == nil {
		t.Fatal("Marshal: want error for valid:false with no Error or Errors, got nil")
	}
}

func TestOutputMarshalRejectsRefWithoutAbsoluteKeywordLocation(t *testing.T) {
	for _, tc := range []struct {
		name string
		kw   string
	}{
		{"$ref", "/items/$ref"},
		{"$dynamicRef", "/items/$dynamicRef"},
		{"nested $ref", "/items/$ref/required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := jsonschema.Output{
				Valid:            false,
				KeywordLocation:  tc.kw,
				InstanceLocation: "",
				Error:            "bad",
			}
			if _, err := json.Marshal(o); err == nil {
				t.Fatalf("Marshal: want error for keywordLocation %q without AbsoluteKeywordLocation", tc.kw)
			}
		})
	}
}

func TestOutputMarshalAcceptsRefWithAbsoluteKeywordLocation(t *testing.T) {
	o := jsonschema.Output{
		Valid:                   false,
		KeywordLocation:         "/items/$ref",
		AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point",
		InstanceLocation:        "/1",
		Error:                   "bad",
	}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(got), `"absoluteKeywordLocation":"https://example.com/polygon#/$defs/point"`) {
		t.Fatalf("Marshal: missing absoluteKeywordLocation: %s", got)
	}
}

func TestOutputMarshalEmitsAnnotation(t *testing.T) {
	o := jsonschema.Output{
		Valid:            true,
		KeywordLocation:  "/properties",
		InstanceLocation: "",
		Annotation:       jsontext.Value(`["a","b"]`),
	}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"valid":true,"keywordLocation":"/properties","instanceLocation":"","annotation":["a","b"]}`
	if eq, err := jsonschema.Equal(got, []byte(want)); err != nil || !eq {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestOutputSourceNotInJSON(t *testing.T) {
	o := jsonschema.Output{
		Valid:            true,
		KeywordLocation:  "",
		InstanceLocation: "",
		Source: jsonschema.Source{
			Name:   "test.json",
			Line:   3,
			Column: 5,
			Offset: 42,
		},
	}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, key := range []string{"source", "Source", "name", "Name", "line", "Line", "column", "Column", "offset", "Offset"} {
		if strings.Contains(string(got), `"`+key+`":`) {
			t.Errorf("JSON should not contain %q field, got: %s", key, got)
		}
	}
}

func TestValidationErrorImplementsError(t *testing.T) {
	var err error = &jsonschema.ValidationError{
		Output: jsonschema.Output{
			Valid:            false,
			KeywordLocation:  "/type",
			InstanceLocation: "/foo",
			Error:            "got string, want number",
		},
	}
	msg := err.Error()
	if !strings.Contains(msg, "got string, want number") {
		t.Errorf("ValidationError.Error() should include the message; got %q", msg)
	}
}

func TestValidationErrorIsAndAs(t *testing.T) {
	ve := &jsonschema.ValidationError{
		Output: jsonschema.Output{
			Valid:            false,
			KeywordLocation:  "/type",
			InstanceLocation: "",
			Error:            "x",
		},
	}
	var target *jsonschema.ValidationError
	if !errors.As(ve, &target) {
		t.Errorf("errors.As(*ValidationError) should succeed")
	}
}

// TestOutputSpecVerboseExample builds the JSON Schema 2020-12 verbose
// example from https://json-schema.org/draft/2020-12/output/verbose-example
// in Go and asserts that MarshalJSON emits structurally equivalent JSON.
func TestOutputSpecVerboseExample(t *testing.T) {
	o := jsonschema.Output{
		Valid: false,
		Errors: []jsonschema.Output{
			{Valid: true, KeywordLocation: "/$defs"},
			{Valid: true, KeywordLocation: "/type"},
			{
				Valid:           false,
				KeywordLocation: "/items",
				Errors: []jsonschema.Output{
					{
						Valid:                   true,
						KeywordLocation:         "/items/$ref",
						AbsoluteKeywordLocation: "https://example.com/polygon#/items/$ref",
						InstanceLocation:        "/0",
						Annotations: []jsonschema.Output{
							{
								Valid:                   true,
								KeywordLocation:         "/items/$ref",
								AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point",
								InstanceLocation:        "/0",
								Annotations: []jsonschema.Output{
									{
										Valid:                   true,
										KeywordLocation:         "/items/$ref/type",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/type",
										InstanceLocation:        "/0",
									},
									{
										Valid:                   true,
										KeywordLocation:         "/items/$ref/properties",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/properties",
										InstanceLocation:        "/0",
									},
									{
										Valid:                   true,
										KeywordLocation:         "/items/$ref/required",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/required",
										InstanceLocation:        "/0",
									},
									{
										Valid:                   true,
										KeywordLocation:         "/items/$ref/additionalProperties",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/additionalProperties",
										InstanceLocation:        "/0",
									},
								},
							},
						},
					},
					{
						Valid:                   false,
						KeywordLocation:         "/items/$ref",
						AbsoluteKeywordLocation: "https://example.com/polygon#/items/$ref",
						InstanceLocation:        "/1",
						Errors: []jsonschema.Output{
							{
								Valid:                   false,
								KeywordLocation:         "/items/$ref",
								AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point",
								InstanceLocation:        "/1",
								Errors: []jsonschema.Output{
									{
										Valid:                   true,
										KeywordLocation:         "/items/$ref/type",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/type",
										InstanceLocation:        "/1",
									},
									{
										Valid:                   true,
										KeywordLocation:         "/items/$ref/properties",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/properties",
										InstanceLocation:        "/1",
									},
									{
										Valid:                   false,
										KeywordLocation:         "/items/$ref/required",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/required",
										InstanceLocation:        "/1",
										Error:                   "missing required",
									},
									{
										Valid:                   false,
										KeywordLocation:         "/items/$ref/additionalProperties",
										AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/additionalProperties",
										InstanceLocation:        "/1",
										Errors: []jsonschema.Output{
											{
												Valid:                   false,
												KeywordLocation:         "/items/$ref/additionalProperties",
												AbsoluteKeywordLocation: "https://example.com/polygon#/$defs/point/additionalProperties",
												InstanceLocation:        "/1/z",
												Error:                   "unexpected property",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			{Valid: false, KeywordLocation: "/minItems", Error: "too few"},
		},
	}
	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Spot-check structural pieces from the spec example. We don't insist
	// on byte-for-byte equality because field order and synthetic Error
	// strings on internal valid:false nodes are implementation-defined.
	for _, want := range []string{
		`"valid":false`,
		`"keywordLocation":"/items"`,
		`"absoluteKeywordLocation":"https://example.com/polygon#/$defs/point"`,
		`"instanceLocation":"/1/z"`,
		`"keywordLocation":"/items/$ref/additionalProperties"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("verbose example output missing %q\nfull JSON: %s", want, got)
		}
	}
}

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

func TestOutputFlag(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/f/flag", []byte(`{
		"$id": "https://example.com/f/flag",
		"type": "integer"
	}`))
	for _, tc := range []struct {
		name string
		in   []byte
		want bool
	}{
		{"valid", []byte(`42`), true},
		{"invalid", []byte(`"x"`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flag := doc.Validate("inst", tc.in).Flag()
			if flag.Valid != tc.want {
				t.Errorf("Flag().Valid = %v, want %v", flag.Valid, tc.want)
			}
			if len(flag.Errors) != 0 || len(flag.Annotations) != 0 || flag.Error != "" {
				t.Errorf("Flag() should have no children or message; got %+v", flag)
			}
			got, err := json.Marshal(flag)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			// Flag form should be a tiny JSON object with valid only
			// (plus the always-required keywordLocation/instanceLocation
			// per the spec output schema).
			if strings.Contains(string(got), `"errors"`) || strings.Contains(string(got), `"annotations"`) {
				t.Errorf("Flag JSON should not contain children: %s", got)
			}
		})
	}
}

func TestOutputBasicFlattens(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/f/basic", []byte(`{
		"$id": "https://example.com/f/basic",
		"properties": {
			"a": { "type": "integer" },
			"b": { "type": "string" }
		}
	}`))
	verbose := doc.Validate("inst", []byte(`{"a":"x","b":1}`))
	if verbose.Valid {
		t.Fatal("expected invalid")
	}
	basic := verbose.Basic()
	if basic.Valid {
		t.Fatal("Basic should preserve Valid")
	}
	// Every entry in basic.Errors must be a leaf (no children of its own).
	for _, e := range basic.Errors {
		if len(e.Errors) != 0 || len(e.Annotations) != 0 {
			t.Errorf("Basic entry should be a leaf; got %+v", e)
		}
	}
	// We expect at least the failing /properties/a/type and /properties/b/type
	// units somewhere in the flat list.
	found := map[string]bool{}
	for _, e := range basic.Errors {
		found[e.KeywordLocation] = true
	}
	for _, want := range []string{"/properties/a/type", "/properties/b/type"} {
		if !found[want] {
			t.Errorf("Basic.Errors missing %q (have %v)", want, found)
		}
	}
}

func TestOutputBasicValidFlattensAnnotations(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/f/basic-valid", []byte(`{
		"$id": "https://example.com/f/basic-valid",
		"title": "T",
		"properties": {
			"a": { "type": "string" }
		}
	}`))
	verbose := doc.Validate("inst", []byte(`{"a":"hi"}`))
	if !verbose.Valid {
		t.Fatal("expected valid")
	}
	basic := verbose.Basic()
	if !basic.Valid {
		t.Fatal("Basic should preserve Valid")
	}
	for _, c := range basic.Annotations {
		if len(c.Errors) != 0 || len(c.Annotations) != 0 {
			t.Errorf("Basic Annotation entry should be a leaf; got %+v", c)
		}
	}
	// Two leaves we know should appear: /title (annotation-only) and
	// the recursive /properties/a/type leaf.
	found := map[string]bool{}
	for _, c := range basic.Annotations {
		found[c.KeywordLocation] = true
	}
	for _, want := range []string{"/title", "/properties/a/type"} {
		if !found[want] {
			t.Errorf("Basic.Annotations missing %q (have %v)", want, found)
		}
	}
}

func TestOutputDetailedPrunesQuietSuccess(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/f/detailed", []byte(`{
		"$id": "https://example.com/f/detailed",
		"type": "object",
		"properties": {
			"a": { "type": "integer" }
		}
	}`))
	verbose := doc.Validate("inst", []byte(`{"a":1}`))
	if !verbose.Valid {
		t.Fatal("expected valid")
	}
	detailed := verbose.Detailed()
	if !detailed.Valid {
		t.Fatal("Detailed should preserve Valid")
	}
	// /type passed but produced no annotation; Detailed should drop it.
	// /properties produced an annotation (matched keys), so it stays.
	for _, c := range detailed.Annotations {
		if c.KeywordLocation == "/type" {
			t.Errorf("Detailed should prune /type with no annotation; got %+v", c)
		}
	}
}

func TestOutputVerboseIsIdentity(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/f/verbose", []byte(`{
		"$id": "https://example.com/f/verbose",
		"type": "integer"
	}`))
	out := doc.Validate("inst", []byte(`42`))
	v := out.Verbose()
	gotA, _ := json.Marshal(out)
	gotB, _ := json.Marshal(v)
	if string(gotA) != string(gotB) {
		t.Errorf("Verbose() should equal Validate() output; got %s vs %s", gotA, gotB)
	}
}
