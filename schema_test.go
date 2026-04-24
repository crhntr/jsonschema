package jsonschema_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-json-experiment/json"

	"github.com/crhntr/jsonschema"
)

func TestMeta(t *testing.T) {
	matches, err := filepath.Glob("docs/2020-12/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		t.Run(filepath.Base(match), func(t *testing.T) {
			buf, err := os.ReadFile(match)
			if err != nil {
				t.Fatal(err)
			}
			var m jsonschema.Meta
			if err := json.Unmarshal(buf, &m); err != nil {
				t.Fatal(err)
			}
		})
	}
}
