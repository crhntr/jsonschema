package jsonschema

import (
	"iter"
	"maps"
	"slices"

	"github.com/go-json-experiment/json"
)

// Parse unmarshals a JSON Schema document and retains a reference to buf so
// callers can recover the original bytes via (*Schema).Source. The returned
// schema is unresolved — call (*Resolver).Resolve or the package-level
// Resolve to dereference $ref / $dynamicRef.
func Parse(buf []byte) (*Schema, error) {
	var s Schema
	if err := json.Unmarshal(buf, &s); err != nil {
		return nil, err
	}
	s.source = buf
	return &s, nil
}

// Resolved returns the lexical-scope target of $ref or $dynamicRef, or nil if
// this subschema has no reference or the schema has not been resolved.
func (m *Schema) Resolved() *Schema { return m.resolved }

// IsDynamic reports whether this subschema's $dynamicRef is "bookended" per
// JSON Schema 2020-12 §8.2.3.2 — i.e., the initial lexical target's resource
// contains a matching $dynamicAnchor, so a validator must walk the dynamic
// scope at validation time.
func (m *Schema) IsDynamic() bool { return m.dynamic }

// BaseURI returns the lexical-scope base URI in effect at this subschema.
func (m *Schema) BaseURI() string { return m.baseURI }

// Resource returns the root of the JSON Schema resource (the nearest
// enclosing schema that defines $id, or the document root) containing this
// subschema. Returns nil before resolution.
func (m *Schema) Resource() *Schema { return m.resource }

// Anchor looks up a $anchor by name within this resource. Only meaningful
// when called on a resource root (m == m.Resource()).
func (m *Schema) Anchor(name string) *Schema { return m.anchors[name] }

// DynamicAnchor looks up a $dynamicAnchor by name within this resource.
func (m *Schema) DynamicAnchor(name string) *Schema { return m.dynamicAnchors[name] }

// Source returns the original JSON document bytes this Schema was parsed from.
// Only populated on resource roots (top-level document or embedded $id
// resources within the same document share the same slice). Returns nil
// otherwise.
func (m *Schema) Source() []byte { return m.source }

// Subschemas yields each direct subschema child of m. Boolean schemas
// (and nil) yield nothing. Children are visited in this order: $defs,
// properties, allOf, anyOf, oneOf, then if / then / else / not / items /
// additionalProperties / propertyNames. Nil singleton slots are skipped.
func (m *Schema) Subschemas() iter.Seq[*Schema] {
	return func(yield func(*Schema) bool) {
		if m == nil {
			return
		}
		obj, ok := m.TypeObject()
		if !ok {
			return
		}
		for _, s := range []iter.Seq[*Schema]{
			maps.Values(obj.Properties),
			maps.Values(obj.Defs),
			maps.Values(obj.PatternProperties),
			maps.Values(obj.DependentSchemas),
			slices.Values(obj.AllOf),
			slices.Values(obj.AnyOf),
			slices.Values(obj.OneOf),
			slices.Values(obj.PrefixItems),
			slices.Values([]*Schema{
				obj.If,
				obj.Then,
				obj.Else,
				obj.Not,
				obj.Items,
				obj.Contains,
				obj.AdditionalProperties,
				obj.UnevaluatedProperties,
				obj.UnevaluatedItems,
				obj.PropertyNames,
				obj.ContentSchema,
			}),
		} {
			for e := range s {
				if !yield(e) {
					return
				}
			}
		}
	}
}
