package jsonschema_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema"
)

// TestValidateSourcePointsAtFailingValue guards issue #15: every
// failure's Source must locate the first byte of the failing value in
// the exact bytes handed to Validate, regardless of nesting depth or
// how much whitespace precedes the value.
func TestValidateSourcePointsAtFailingValue(t *testing.T) {
	schema, err := jsonschema.Parse([]byte(`{
	  "type": "object",
	  "properties": {
	    "brokers": {"type": "array", "items": {
	      "type": "object",
	      "properties": {
	        "client_id": {"type": "string"},
	        "subscriptions": {"type": "array", "items": {
	          "type": "object",
	          "properties": {"qos": {"type": "integer", "maximum": 2}},
	          "unevaluatedProperties": {"type": "string"}
	        }},
	        "tags": {"prefixItems": [{"type": "string"}], "unevaluatedItems": {"type": "string"}}
	      }
	    }},
	    "name": {"type": "string"}
	  }
	}`))
	if err != nil {
		t.Fatal(err)
	}

	// Whitespace is deliberately irregular: leading whitespace before
	// the root, tabs and multiple spaces around colons and commas.
	src := []byte("\n\n  {\n  \"name\" :\t 42,\n  \"brokers\": [\n    {\n      \"client_id\":     7,\n      \"host\": \"h\",\n      \"subscriptions\": [\n        {\"topic_filter\": \"a\", \"qos\": 3, \"extra\":\n\n   false}\n      ],\n      \"tags\": [\"x\",   99]\n    }\n  ]\n}\n")

	// instance location → the exact text of the value that failed.
	want := map[string]string{
		"/name":                            `42`,
		"/brokers/0/client_id":             `7`,
		"/brokers/0/subscriptions/0/qos":   `3`,
		"/brokers/0/subscriptions/0/extra": `false`,
		"/brokers/0/tags/1":                `99`,
	}

	out := schema.Validate("probe", src)
	if out.Valid {
		t.Fatal("expected invalid")
	}
	seen := map[string]bool{}
	for _, leaf := range out.Basic().Errors {
		if leaf.Valid {
			continue
		}
		wantText, ok := want[leaf.InstanceLocation]
		if !ok {
			t.Errorf("unexpected failure at %q: %s", leaf.InstanceLocation, leaf.Error)
			continue
		}
		seen[leaf.InstanceLocation] = true
		off := leaf.Source.Offset
		if off < 0 || off >= int64(len(src)) {
			t.Errorf("%s: Source.Offset = %d, out of range for %d-byte input", leaf.InstanceLocation, off, len(src))
			continue
		}
		if !bytes.HasPrefix(src[off:], []byte(wantText)) {
			t.Errorf("%s: src[Source.Offset=%d:] = %q, want prefix %q", leaf.InstanceLocation, off, truncate(src[off:], 12), wantText)
		}
		if leaf.Source.Name != "probe" {
			t.Errorf("%s: Source.Name = %q, want %q", leaf.InstanceLocation, leaf.Source.Name, "probe")
		}
		if got, want := leaf.Source, jsonschema.NewSource("probe", src, off); got != want {
			t.Errorf("%s: Source = %+v, want NewSource(%d) = %+v", leaf.InstanceLocation, got, off, want)
		}
	}
	for loc := range want {
		if !seen[loc] {
			t.Errorf("no failure reported at %q", loc)
		}
	}
}

// TestValidateSourceRootWithLeadingWhitespace checks that a failure on
// the root value itself points at the root value, not at byte 0, when
// the document starts with whitespace.
func TestValidateSourceRootWithLeadingWhitespace(t *testing.T) {
	schema, err := jsonschema.Parse([]byte(`{"type": "string"}`))
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("\n\n\t  [1]\n")
	out := schema.Validate("probe", src)
	leaf := findLeaf(out, "/type")
	if leaf == nil {
		t.Fatalf("no leaf at /type; got %+v", out)
	}
	wantOff := int64(strings.Index(string(src), "["))
	if leaf.Source.Offset != wantOff {
		t.Errorf("Source.Offset = %d, want %d", leaf.Source.Offset, wantOff)
	}
	if got, want := leaf.Source, jsonschema.NewSource("probe", src, wantOff); got != want {
		t.Errorf("Source = %+v, want %+v", got, want)
	}
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}
