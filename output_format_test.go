package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/go-json-experiment/json"
)

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
