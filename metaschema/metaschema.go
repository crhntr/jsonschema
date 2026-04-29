// Package metaschema embeds the published JSON Schema 2020-12
// meta-schema documents (the root schema plus the seven vocabulary
// files) so they can be served from a binary without network or
// filesystem access. The Seed helper preloads them into a Resolver
// keyed by their declared $id, after which any reference to
// https://json-schema.org/draft/2020-12/schema (or its vocabulary
// siblings) resolves locally.
package metaschema

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/crhntr/jsonschema"
)

//go:embed draft/2020-12/schema.json draft/2020-12/meta/*.json
var fsRoot embed.FS

// FS is the read-only filesystem of every embedded meta-schema
// document. The directory layout mirrors the upstream URL space:
// draft/2020-12/schema.json is the root, draft/2020-12/meta/<vocab>.json
// is each vocabulary file.
var FS fs.FS = fsRoot

// Seed loads every embedded document into r using its declared $id
// as the cache key. After Seed returns, r.Resolve("https://json-schema.org/draft/2020-12/schema")
// (and the seven vocabulary URLs) hits the embedded copy without
// touching the network.
func Seed(r *jsonschema.Resolver) error {
	return fs.WalkDir(fsRoot, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		body, err := fsRoot.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var probe struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if probe.ID == "" {
			return fmt.Errorf("embedded %s has no $id", path)
		}
		if _, err := r.Load(probe.ID, body); err != nil {
			return fmt.Errorf("seed %s: %w", probe.ID, err)
		}
		return nil
	})
}

// SchemaURI is the canonical URL of the JSON Schema 2020-12
// meta-schema's root document.
const SchemaURI = "https://json-schema.org/draft/2020-12/schema"
