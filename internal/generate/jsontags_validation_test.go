package generate

import (
	"strings"
	"testing"
)

// TestDerive_GoJSONTagsFormatRejected rejects a goJSONTags entry using
// the `format` tag option. encoding/json/v2 shipped in Go 1.27 without
// `format` support (https://go.dev/issue/79071): any struct containing
// a format-tagged field fails at runtime with "unsupported `format`
// tag option", so the generator must fail at generate time instead.
func TestDerive_GoJSONTagsFormatRejected(t *testing.T) {
	src := `{
		"type": "object",
		"properties": {
			"when": {
				"type": "string",
				"goType": "time.Time",
				"goImports": ["time"],
				"goJSONTags": ["format:RFC3339Nano"]
			}
		}
	}`
	_, err := deriveObject(t, src)
	if err == nil {
		t.Fatalf("Derive: err = nil, want error for format goJSONTags entry")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("Derive: err = %v, want mention of the format tag option", err)
	}
}

// TestDerive_GoJSONTagsInlineRejected rejects the experiment-era
// `inline` (and `unknown`) tag options, which Go 1.27 renamed to
// `embed` and now silently ignores — a named field carrying them would
// quietly serialize as a regular member.
func TestDerive_GoJSONTagsInlineRejected(t *testing.T) {
	src := `{
		"type": "object",
		"properties": {
			"extra": {
				"type": "object",
				"goJSONTags": ["inline"]
			}
		}
	}`
	_, err := deriveObject(t, src)
	if err == nil {
		t.Fatalf("Derive: err = nil, want error for inline goJSONTags entry")
	}
	if !strings.Contains(err.Error(), "embed") {
		t.Errorf("Derive: err = %v, want mention of the embed replacement", err)
	}
}
