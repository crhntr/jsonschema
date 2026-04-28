package jsonschema

import (
	"fmt"
	"strings"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// Output is a JSON Schema 2020-12 Output Unit (§12.4) plus a Source
// extension. The exported fields are a structural superset of the spec's
// outputUnit: Valid, KeywordLocation, AbsoluteKeywordLocation,
// InstanceLocation, Error, Errors, Annotation, and Annotations carry the
// spec data; Source records the byte position of InstanceLocation in the
// validated document and is omitted from the JSON form.
//
// Validate / ValidateWithFormatAssertion always return the verbose tree
// (one node per evaluated keyword). Use Flag, Basic, Detailed, or Verbose
// to derive the other spec output formats.
type Output struct {
	Valid                   bool           `json:"valid"`
	KeywordLocation         string         `json:"keywordLocation"`
	AbsoluteKeywordLocation string         `json:"absoluteKeywordLocation,omitempty"`
	InstanceLocation        string         `json:"instanceLocation"`
	Error                   string         `json:"error,omitempty"`
	Errors                  []Output       `json:"errors,omitempty"`
	Annotation              jsontext.Value `json:"annotation,omitempty"`
	Annotations             []Output       `json:"annotations,omitempty"`
	Source                  Source         `json:"-"`
}

// Source records where in the validated document the failing or
// annotated value lives. It is the library's superset extension over
// the spec's outputUnit and is excluded from the JSON form.
type Source struct {
	Name   string
	Line   int64
	Column int64
	Offset int64
}

// MarshalJSONTo emits a spec-conformant outputUnit. It enforces two
// invariants required by the 2020-12 output schema and returns an error
// when either is violated, so a buggy validator surfaces at the marshal
// boundary rather than producing silently invalid output.
func (o Output) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := o.checkOutputInvariants(); err != nil {
		return err
	}
	type alias Output
	return json.MarshalEncode(enc, alias(o))
}

func (o Output) checkOutputInvariants() error {
	if !o.Valid && o.Error == "" && len(o.Errors) == 0 {
		return fmt.Errorf("jsonschema: invalid Output at keywordLocation %q: valid:false requires Error or Errors", o.KeywordLocation)
	}
	if keywordLocationCrossedReference(o.KeywordLocation) && o.AbsoluteKeywordLocation == "" {
		return fmt.Errorf("jsonschema: invalid Output: keywordLocation %q crosses a $ref/$dynamicRef but AbsoluteKeywordLocation is empty", o.KeywordLocation)
	}
	return nil
}

// keywordLocationCrossedReference reports whether kw contains a $ref or
// $dynamicRef segment — the condition under which the 2020-12 output
// schema requires absoluteKeywordLocation.
func keywordLocationCrossedReference(kw string) bool {
	if kw == "" {
		return false
	}
	for seg := range strings.SplitSeq(kw, "/") {
		if seg == "$ref" || seg == "$dynamicRef" {
			return true
		}
	}
	return false
}

// ValidationError adapts a failed Output to the error interface so
// validation results can flow through Go's idiomatic error returns.
type ValidationError struct{ Output }

// Error returns a human-readable description by walking the Output
// tree and joining every leaf failure message it finds. When the
// Output's Source.Name is non-empty, the message is prefixed with
// `<name>:<line>:<column>: ` so failures from a named document carry
// position context (the same shape the previous ErrorWithPosition
// type produced). When no leaf has a message, falls back to a
// synthetic summary so callers always see something actionable.
func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	msgs := collectErrorMessages(e.Output)
	if len(msgs) == 0 {
		msgs = []string{fmt.Sprintf("validation failed at keywordLocation=%q instanceLocation=%q",
			e.Output.KeywordLocation, e.Output.InstanceLocation)}
	}
	body := strings.Join(msgs, "; ")
	if e.Output.Source.Name != "" {
		return fmt.Sprintf("%s:%d:%d: %s", e.Output.Source.Name, e.Output.Source.Line, e.Output.Source.Column, body)
	}
	return body
}

// collectErrorMessages returns every Error string found in the failure
// tree rooted at o (preorder). Ignores valid:true subtrees.
func collectErrorMessages(o Output) []string {
	if o.Valid {
		return nil
	}
	var msgs []string
	if o.Error != "" {
		if o.InstanceLocation != "" {
			msgs = append(msgs, fmt.Sprintf("%s: %s", o.InstanceLocation, o.Error))
		} else {
			msgs = append(msgs, o.Error)
		}
	}
	for _, c := range o.Errors {
		msgs = append(msgs, collectErrorMessages(c)...)
	}
	return msgs
}

// AsError returns a *ValidationError wrapping o when o.Valid is false,
// or nil when o.Valid is true. Convenient for callers who want a Go
// error from Validate's Output.
func (o Output) AsError() error {
	if o.Valid {
		return nil
	}
	return &ValidationError{Output: o}
}

var _ error = (*ValidationError)(nil)
