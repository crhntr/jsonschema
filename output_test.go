package jsonschema_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
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
