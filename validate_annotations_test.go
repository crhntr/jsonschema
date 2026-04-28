package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/go-json-experiment/json"

	"github.com/crhntr/jsonschema"
)

// findValidKeyword walks o (preorder, descending into both Errors and
// Annotations) and returns the first valid:true Output whose
// KeywordLocation equals exactly kw, or nil if none.
func findValidKeyword(o jsonschema.Output, kw string) *jsonschema.Output {
	if o.Valid && o.KeywordLocation == kw {
		out := o
		return &out
	}
	for _, c := range o.Annotations {
		if got := findValidKeyword(c, kw); got != nil {
			return got
		}
	}
	for _, c := range o.Errors {
		if got := findValidKeyword(c, kw); got != nil {
			return got
		}
	}
	return nil
}

func TestAnnotationProperties(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/props", []byte(`{
		"$id": "https://example.com/ann/props",
		"properties": {
			"a": { "type": "string" },
			"b": { "type": "integer" }
		}
	}`))
	out := doc.Validate("inst", []byte(`{"a":"hi","b":42}`))
	if !out.Valid {
		t.Fatalf("expected valid; tree: %+v", out)
	}
	props := findValidKeyword(out, "/properties")
	if props == nil {
		t.Fatalf("missing valid /properties unit; tree: %+v", out)
	}
	if len(props.Annotation) == 0 {
		t.Fatal("/properties.Annotation should list matched keys")
	}
	var matched []string
	if err := json.Unmarshal(props.Annotation, &matched); err != nil {
		t.Fatalf("Unmarshal annotation: %v (%s)", err, props.Annotation)
	}
	if len(matched) != 2 {
		t.Errorf("matched keys = %v, want 2", matched)
	}
	if len(props.Annotations) != 2 {
		t.Errorf("/properties.Annotations len = %d, want 2 child units", len(props.Annotations))
	}
}

func TestAnnotationTitleAndDescription(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/meta", []byte(`{
		"$id": "https://example.com/ann/meta",
		"title": "My Schema",
		"description": "An example.",
		"type": "string"
	}`))
	out := doc.Validate("inst", []byte(`"hello"`))
	if !out.Valid {
		t.Fatalf("expected valid; tree: %+v", out)
	}
	title := findValidKeyword(out, "/title")
	if title == nil {
		t.Fatalf("missing /title; tree: %+v", out)
	}
	if !strings.Contains(string(title.Annotation), `"My Schema"`) {
		t.Errorf("/title.Annotation = %s, want JSON \"My Schema\"", title.Annotation)
	}
	desc := findValidKeyword(out, "/description")
	if desc == nil || !strings.Contains(string(desc.Annotation), `"An example."`) {
		t.Errorf("missing or wrong /description; got %+v", desc)
	}
}

func TestAnnotationFormatNotAsserting(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/fmt", []byte(`{
		"$id": "https://example.com/ann/fmt",
		"format": "uuid"
	}`))
	// Validate (no format assertion) — bad uuid string should still be valid.
	out := doc.Validate("inst", []byte(`"not-a-uuid"`))
	if !out.Valid {
		t.Fatalf("expected valid (format is annotation-only); tree: %+v", out)
	}
	fmt := findValidKeyword(out, "/format")
	if fmt == nil {
		t.Fatalf("missing /format unit; tree: %+v", out)
	}
	if !strings.Contains(string(fmt.Annotation), `"uuid"`) {
		t.Errorf("/format.Annotation = %s, want \"uuid\"", fmt.Annotation)
	}
}

func TestAnnotationFormatAssertingDoesNotAnnotate(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/fmta", []byte(`{
		"$id": "https://example.com/ann/fmta",
		"format": "uuid"
	}`))
	out := doc.ValidateWithFormatAssertion("inst", []byte(`"550e8400-e29b-41d4-a716-446655440000"`))
	if !out.Valid {
		t.Fatalf("expected valid uuid; tree: %+v", out)
	}
	// Spec §F.2: under format-assertion, /format is an assertion and
	// must not emit an annotation.
	fmt := findValidKeyword(out, "/format")
	if fmt == nil {
		t.Fatalf("expected /format unit; tree: %+v", out)
	}
	if len(fmt.Annotation) != 0 {
		t.Errorf("/format should have no annotation under assertion; got %s", fmt.Annotation)
	}
}

func TestAnnotationContains(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/contains", []byte(`{
		"$id": "https://example.com/ann/contains",
		"contains": { "type": "integer" }
	}`))
	out := doc.Validate("inst", []byte(`["a", 1, "b", 2]`))
	if !out.Valid {
		t.Fatalf("expected valid; tree: %+v", out)
	}
	contains := findValidKeyword(out, "/contains")
	if contains == nil {
		t.Fatalf("missing /contains; tree: %+v", out)
	}
	var idx []int
	if err := json.Unmarshal(contains.Annotation, &idx); err != nil {
		t.Fatalf("Unmarshal annotation: %v (%s)", err, contains.Annotation)
	}
	if len(idx) != 2 || idx[0] != 1 || idx[1] != 3 {
		t.Errorf("matched indices = %v, want [1 3]", idx)
	}
}

func TestAnnotationItemsTrue(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/items", []byte(`{
		"$id": "https://example.com/ann/items",
		"items": { "type": "integer" }
	}`))
	out := doc.Validate("inst", []byte(`[1, 2, 3]`))
	if !out.Valid {
		t.Fatalf("expected valid; tree: %+v", out)
	}
	items := findValidKeyword(out, "/items")
	if items == nil {
		t.Fatalf("missing /items; tree: %+v", out)
	}
	if string(items.Annotation) != "true" {
		t.Errorf("/items.Annotation = %s, want true", items.Annotation)
	}
}

func TestAnnotationIfAlwaysEmits(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/if", []byte(`{
		"$id": "https://example.com/ann/if",
		"if":   { "type": "string" },
		"then": { "minLength": 1 }
	}`))
	for _, body := range []string{`"x"`, `42`} {
		out := doc.Validate("inst", []byte(body))
		if !out.Valid {
			t.Fatalf("expected valid for %s; tree: %+v", body, out)
		}
		ifUnit := findValidKeyword(out, "/if")
		if ifUnit == nil {
			t.Fatalf("expected /if unit for body %s; tree: %+v", body, out)
		}
		if string(ifUnit.Annotation) != "true" {
			t.Errorf("/if.Annotation = %s, want true (per spec §10.2.2.1)", ifUnit.Annotation)
		}
	}
}

func TestAnnotationAnyOfOnlyMatching(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/anyof", []byte(`{
		"$id": "https://example.com/ann/anyof",
		"anyOf": [
			{ "type": "string" },
			{ "type": "integer" }
		]
	}`))
	out := doc.Validate("inst", []byte(`"hi"`))
	if !out.Valid {
		t.Fatal("expected valid")
	}
	any := findValidKeyword(out, "/anyOf")
	if any == nil {
		t.Fatalf("missing /anyOf; tree: %+v", out)
	}
	// Only one branch matched; spec says only that branch's
	// annotations propagate.
	if len(any.Annotations) != 1 {
		t.Errorf("/anyOf.Annotations len = %d, want 1 (matching branch only)", len(any.Annotations))
	}
}

func TestAnnotationOneOfOnlyMatching(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/oneof", []byte(`{
		"$id": "https://example.com/ann/oneof",
		"oneOf": [
			{ "type": "string" },
			{ "type": "integer" }
		]
	}`))
	out := doc.Validate("inst", []byte(`42`))
	if !out.Valid {
		t.Fatal("expected valid")
	}
	one := findValidKeyword(out, "/oneOf")
	if one == nil {
		t.Fatalf("missing /oneOf; tree: %+v", out)
	}
	if len(one.Annotations) != 1 {
		t.Errorf("/oneOf.Annotations len = %d, want 1 (matching branch only)", len(one.Annotations))
	}
}

func TestAnnotationNotHasNoAnnotationChildren(t *testing.T) {
	doc := resolveBytes(t, "https://example.com/ann/not", []byte(`{
		"$id": "https://example.com/ann/not",
		"not": { "type": "string" }
	}`))
	out := doc.Validate("inst", []byte(`42`))
	if !out.Valid {
		t.Fatal("expected valid (42 is not a string)")
	}
	not := findValidKeyword(out, "/not")
	if not == nil {
		t.Fatalf("missing /not; tree: %+v", out)
	}
	if len(not.Annotations) != 0 {
		t.Errorf("/not.Annotations should be empty (spec §10.2.1.4 — annotation undefined); got %+v", not.Annotations)
	}
}

func TestAnnotationUnknownKeyword(t *testing.T) {
	// "x-mycustom" isn't a known keyword; per spec §3.5.4 it should
	// surface as an annotation in verbose output.
	doc := resolveBytes(t, "https://example.com/ann/unk", []byte(`{
		"$id": "https://example.com/ann/unk",
		"type": "string",
		"x-mycustom": "hello",
		"x-other": [1, 2]
	}`))
	out := doc.Validate("inst", []byte(`"abc"`))
	if !out.Valid {
		t.Fatalf("expected valid; tree: %+v", out)
	}
	custom := findValidKeyword(out, "/x-mycustom")
	if custom == nil {
		t.Fatalf("missing /x-mycustom annotation; tree: %+v", out)
	}
	if !strings.Contains(string(custom.Annotation), `"hello"`) {
		t.Errorf("/x-mycustom.Annotation = %s, want \"hello\"", custom.Annotation)
	}
	other := findValidKeyword(out, "/x-other")
	if other == nil {
		t.Fatalf("missing /x-other annotation; tree: %+v", out)
	}
	var arr []int
	if err := json.Unmarshal(other.Annotation, &arr); err != nil {
		t.Fatalf("Unmarshal /x-other.Annotation: %v (%s)", err, other.Annotation)
	}
	if len(arr) != 2 || arr[0] != 1 || arr[1] != 2 {
		t.Errorf("/x-other.Annotation = %v, want [1 2]", arr)
	}
}

func TestVerboseTreeMarshalsCleanly(t *testing.T) {
	// A schema that exercises a few annotation-producing keywords and
	// always passes; the marshaled output must be a spec-valid Output
	// Unit (MarshalJSON enforces the invariants).
	doc := resolveBytes(t, "https://example.com/ann/all", []byte(`{
		"$id": "https://example.com/ann/all",
		"title": "T",
		"properties": { "n": { "type": "integer" } }
	}`))
	out := doc.Validate("inst", []byte(`{"n":1}`))
	if !out.Valid {
		t.Fatalf("expected valid; tree: %+v", out)
	}
	got, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(got), `"annotations"`) {
		t.Errorf("verbose output should include annotations array; got %s", got)
	}
	if !strings.Contains(string(got), `"annotation"`) {
		t.Errorf("verbose output should include keyword-level annotation values; got %s", got)
	}
}
