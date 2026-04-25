package jsonschema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/crhntr/jsonschema"
)

// TestConformanceLoadsCleanly walks the JSON Schema 2020-12 conformance
// suite and parses every schema in every test case, asserting that the
// jsonschema.Schema model accepts the full range of constructs the suite
// uses. This is a *load-only* pass — it does not validate instances or
// resolve external $refs; that's the next layer of conformance.
func TestConformanceLoadsCleanly(t *testing.T) {
	type conformanceCase struct {
		Description string         `json:"description"`
		Schema      jsontext.Value `json:"schema"`
		Tests       []struct {
			Description string         `json:"description"`
			Data        jsontext.Value `json:"data"`
			Valid       bool           `json:"valid"`
		} `json:"tests"`
	}

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

func Test(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entrypoint string
		minSchemas int
	}{
		{
			name:       "2020-12 meta-schema",
			entrypoint: "https://json-schema.org/draft/2020-12/schema",
			minSchemas: 8, // schema.json + 7 vocab files
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := startSchemaServer(t)
			schema, err := jsonschema.Resolve(t.Context(), client, tc.entrypoint)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.entrypoint, err)
			}
			if schema == nil {
				t.Fatal("Resolve returned nil schema")
			}
			if got := schema.BaseURI(); got != tc.entrypoint {
				t.Errorf("BaseURI = %q, want %q", got, tc.entrypoint)
			}
			if len(schema.Source()) == 0 {
				t.Errorf("Source() empty; want bytes from response")
			}

			// Every $ref in allOf should resolve to a non-nil target whose
			// BaseURI matches the referenced meta/* document.
			obj, ok := schema.TypeObject()
			if !ok {
				t.Fatal("schema is not an object")
			}
			if len(obj.AllOf) == 0 {
				t.Fatal("schema.allOf is empty")
			}
			for i, sub := range obj.AllOf {
				resolved := sub.Resolved()
				if resolved == nil {
					t.Errorf("allOf[%d] %q: Resolved() is nil", i, mustObject(t, sub).Ref)
					continue
				}
				if resolved.BaseURI() == "" {
					t.Errorf("allOf[%d]: target BaseURI empty", i)
				}
			}

			// Verify $dynamicRef "#meta" inside meta/applicator is bookended.
			applicator := findResolved(t, schema, "meta/applicator")
			if applicator == nil {
				t.Fatal("could not find meta/applicator via allOf")
			}
			dyn := findFirstDynamicRef(applicator)
			if dyn == nil {
				t.Fatal("no $dynamicRef found in applicator subtree")
			}
			if !dyn.IsDynamic() {
				t.Errorf("$dynamicRef %q: expected IsDynamic() true", mustObject(t, dyn).DynamicRef)
			}
			if dyn.Resolved() == nil {
				t.Fatal("$dynamicRef Resolved() is nil")
			}
			// Lexical fallback target's resource should declare the
			// $dynamicAnchor we're looking up.
			anchorName := strings.TrimPrefix(mustObject(t, dyn).DynamicRef, "#")
			if dyn.Resolved().Resource().DynamicAnchor(anchorName) == nil {
				t.Errorf("target resource missing $dynamicAnchor %q", anchorName)
			}
		})
	}
}
