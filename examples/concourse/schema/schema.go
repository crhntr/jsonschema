// Package schema holds the JSON Schema for Concourse pipelines. Its $defs
// for job, resource and resource_type are also the roots of the editor's
// forms (see jsonhandler.NewAt).
package schema

import (
	"context"
	_ "embed"

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
