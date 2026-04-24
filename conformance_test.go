package jsonschema_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/crhntr/jsonschema"
)

// TestConformanceLoadsCleanly walks the JSON Schema 2020-12 conformance
// suite and parses every schema in every test case, asserting that the
// jsonschema.Meta model accepts the full range of constructs the suite
// uses. This is a *load-only* pass — it does not validate instances or
// resolve external $refs; that's the next layer of conformance.
func TestConformanceLoadsCleanly(t *testing.T) {
	matches, err := filepath.Glob("testdata/JSON-Schema-Test-Suite/tests-draft2020-12/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no conformance test files found")
	}

	for _, file := range matches {
		t.Run(filepath.Base(file), func(t *testing.T) {
			buf, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var cases []conformanceCase
			if err := json.Unmarshal(buf, &cases); err != nil {
				t.Fatalf("decode %s: %v", file, err)
			}
			for _, c := range cases {
				t.Run(c.Description, func(t *testing.T) {
					if _, err := jsonschema.Parse(c.Schema); err != nil {
						t.Errorf("Parse: %v\nschema: %s", err, c.Schema)
					}
				})
			}
		})
	}
}

type conformanceCase struct {
	Description string         `json:"description"`
	Schema      jsontext.Value `json:"schema"`
	Tests       []struct {
		Description string         `json:"description"`
		Data        jsontext.Value `json:"data"`
		Valid       bool           `json:"valid"`
	} `json:"tests"`
}
