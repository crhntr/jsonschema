// Package schema holds the JSON Schema for Concourse pipelines and builds
// the narrower schemas the editor's forms use.
package schema

import (
	"context"
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/crhntr/jsonschema"
)

//go:embed pipeline.json
var pipeline []byte

// URI is the pipeline schema's $id.
const URI = "https://example.com/concourse/pipeline.json"

// Pipeline returns the schema document for a whole pipeline.
func Pipeline() []byte { return pipeline }

// Load returns the pipeline schema with its references resolved.
func Load(ctx context.Context) (*jsonschema.Schema, error) {
	r := jsonschema.NewResolver(nil)
	if _, err := r.Load(URI, pipeline); err != nil {
		return nil, err
	}
	return r.Resolve(ctx, URI)
}

// Definition returns a schema document whose root is one of the pipeline
// schema's definitions, such as "job", for a form that edits only that
// part of a pipeline.
func Definition(name string) ([]byte, error) {
	var doc struct {
		Schema string                    `json:"$schema"`
		Defs   map[string]jsontext.Value `json:"$defs"`
	}
	if err := json.Unmarshal(pipeline, &doc); err != nil {
		return nil, err
	}
	if _, ok := doc.Defs[name]; !ok {
		return nil, fmt.Errorf("no definition %q", name)
	}
	return json.Marshal(map[string]any{
		"$schema": doc.Schema,
		"$ref":    "#/$defs/" + name,
		"$defs":   doc.Defs,
	}, json.Deterministic(true))
}
