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
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"strings"

	"github.com/crhntr/jsonschema"
)

// SchemaURI is the canonical URL of the JSON Schema 2020-12
// meta-schema's root document.
const SchemaURI = "https://json-schema.org/draft/2020-12/schema"

//go:embed draft/2020-12/schema.json draft/2020-12/meta/*.json
var fsRoot embed.FS

// embedded maps each document's declared $id to its bytes, populated
// once at package init by walking fsRoot. Keeping the mapping
// derived from the embed (rather than a hand-maintained URL → path
// table) means dropping a new vocabulary file into draft/2020-12/
// requires no code changes.
var embedded = func() map[string][]byte {
	m := map[string][]byte{}
	err := fs.WalkDir(fsRoot, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
		m[probe.ID] = body
		return nil
	})
	if err != nil {
		panic(fmt.Errorf("metaschema: index embedded documents: %w", err))
	}
	return m
}()

// BytesForURL returns the bytes of the embedded meta-schema document
// whose $id equals url, or false if no such document is shipped.
func BytesForURL(url string) ([]byte, bool) {
	body, ok := embedded[url]
	return body, ok
}

// Seed loads every embedded document into r using its declared $id
// as the cache key. After Seed returns, r.Resolve(SchemaURI) (and
// the seven vocabulary URLs) hits the embedded copy without
// touching the network.
func Seed(r *jsonschema.Resolver) error {
	for uri, body := range embedded {
		if _, err := r.Load(uri, body); err != nil {
			return fmt.Errorf("seed %s: %w", uri, err)
		}
	}
	return nil
}
