// Package jsonschema is a JSON Schema 2020-12 toolkit for Go.
//
// The package provides three concentric layers:
//
//   - Parse turns raw JSON Schema bytes into a [*Schema] without
//     resolving any references. The returned tree exposes every
//     keyword as a Go field on [SchemaObject] (or as an opaque value
//     under SchemaObject.Extra for keywords this library does not
//     recognize).
//
//   - [Resolver] fetches and links a transitive closure of schema
//     resources, populating $ref / $dynamicRef / $anchor /
//     $dynamicAnchor pointers and applying the metaschema's
//     $vocabulary declaration.
//
//   - [*Schema.Validate] runs an instance through a resolved schema
//     and returns an [Output] tree shaped per JSON Schema 2020-12
//     §12.4 (Output). Output is a structural superset of the spec's
//     outputUnit — same fields plus a [Source] byte-position
//     extension — and can be projected to any of the four spec
//     formats via [Output.Flag], [Output.Basic], [Output.Detailed],
//     and [Output.Verbose].
//
// Validate targets JSON Schema 2020-12 specifically. Pre-2020-12
// dialects are best-effort: the declared $schema URI surfaces in
// the /$schema annotation, prefixItems is skipped, but other dialect
// differences (draft-07 lacks unevaluatedProperties, draft-04 lacks
// const, etc.) are not emulated.
package jsonschema

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

// Schema is a parsed JSON Schema document or subschema. A Schema
// holds either a boolean schema (true / false) or an object schema
// ([SchemaObject]); use [*Schema.TypeBoolean] and [*Schema.TypeObject]
// to discriminate. The zero value is invalid; obtain a Schema from
// [Parse] or by walking another Schema.
//
// Resolution metadata (resolved $ref target, base URI, owning
// resource, anchor maps) is populated by [*Resolver.Resolve] and is
// nil on freshly-parsed values.
type Schema struct {
	isBool, isObject bool
	bool             bool
	object           SchemaObject

	// resolution metadata. zero on freshly-unmarshaled values; populated by Resolve.
	resolved       *Schema
	dynamic        bool
	baseURI        string
	pathInResource string // RFC 6901 pointer from resource root to this schema; "" for the resource root
	resource       *Schema
	anchors        map[string]*Schema
	dynamicAnchors map[string]*Schema
	source         []byte
	// vocabularies is populated on resource roots by applyVocabularies
	// from the metaschema's $vocabulary declaration. nil means the
	// resource takes the default 2020-12 set. A non-nil map gates
	// keywords from each vocabulary independently.
	vocabularies map[string]bool
}

func (m *Schema) unsetIs() {
	m.isBool = false
	m.isObject = false
}

// PathInResource returns the RFC 6901 JSON Pointer from this subschema's
// resource root to itself. Empty for the resource root; reset to empty
// when an embedded $id opens a new resource. Used to construct
// absoluteKeywordLocation values for spec-compliant validation output.
func (m *Schema) PathInResource() string { return m.pathInResource }

// SchemaObject is the field-by-field representation of a JSON Schema
// 2020-12 object schema. Each named keyword is a Go field with the
// matching json tag; keywords this library does not recognize are
// captured by Extra so JSON-pointer navigation still reaches them
// and round-tripping preserves them.
//
// SchemaObject is the body type behind [*Schema.TypeObject].
type SchemaObject struct {
	ID     string `json:"$id,omitempty"`
	Schema string `json:"$schema,omitempty"`

	Ref           string `json:"$ref,omitempty"`
	Anchor        string `json:"$anchor,omitempty"`
	DynamicRef    string `json:"$dynamicRef,omitempty"`
	DynamicAnchor string `json:"$dynamicAnchor,omitempty"`

	Vocabulary map[string]bool `json:"$vocabulary,omitempty"`

	Comment string `json:"$comment,omitempty"`

	Defs map[string]*Schema `json:"$defs,omitempty"`

	If   *Schema `json:"if,omitempty"`
	Then *Schema `json:"then,omitempty"`
	Else *Schema `json:"else,omitempty"`

	AllOf []*Schema `json:"allOf,omitempty"`
	AnyOf []*Schema `json:"anyOf,omitempty"`
	OneOf []*Schema `json:"oneOf,omitempty"`
	Not   *Schema   `json:"not,omitempty"`

	Properties            map[string]*Schema  `json:"properties,omitempty"`
	PatternProperties     map[string]*Schema  `json:"patternProperties,omitempty"`
	AdditionalProperties  *Schema             `json:"additionalProperties,omitempty"`
	UnevaluatedProperties *Schema             `json:"unevaluatedProperties,omitempty"`
	PropertyNames         *Schema             `json:"propertyNames,omitempty"`
	DependentRequired     map[string][]string `json:"dependentRequired,omitempty"`
	DependentSchemas      map[string]*Schema  `json:"dependentSchemas,omitempty"`

	// Dependencies is the pre-2020-12 keyword. Each value may be an
	// array of property names (treated like dependentRequired) or a
	// schema (treated like dependentSchemas).
	Dependencies map[string]*Dependency `json:"dependencies,omitempty"`

	// meta-data.json
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Deprecated  bool             `json:"deprecated,omitempty"`
	ReadOnly    bool             `json:"readOnly,omitempty"`
	WriteOnly   bool             `json:"writeOnly,omitempty"`
	Examples    []jsontext.Value `json:"examples,omitempty"`

	PrefixItems      []*Schema `json:"prefixItems,omitempty"`
	Items            *Schema   `json:"items,omitempty"`
	UnevaluatedItems *Schema   `json:"unevaluatedItems,omitempty"`
	Contains         *Schema   `json:"contains,omitempty"`

	MinContains jsontext.Value `json:"minContains,omitempty"`
	MaxContains jsontext.Value `json:"maxContains,omitempty"`

	Type    *Type            `json:"type,omitempty"`
	Enum    []jsontext.Value `json:"enum,omitempty"`
	Const   jsontext.Value   `json:"const,omitempty"`
	Default jsontext.Value   `json:"default,omitempty"`

	MultipleOf       jsontext.Value `json:"multipleOf,omitempty"`
	Maximum          jsontext.Value `json:"maximum,omitempty"`
	ExclusiveMaximum jsontext.Value `json:"exclusiveMaximum,omitempty"`
	Minimum          jsontext.Value `json:"minimum,omitempty"`
	ExclusiveMinimum jsontext.Value `json:"exclusiveMinimum,omitempty"`
	MaxLength        jsontext.Value `json:"maxLength,omitempty"`
	MinLength        jsontext.Value `json:"minLength,omitempty"`

	MaxItems    jsontext.Value `json:"maxItems,omitempty"`
	MinItems    jsontext.Value `json:"minItems,omitempty"`
	UniqueItems bool           `json:"uniqueItems,omitempty"`

	MaxProperties jsontext.Value `json:"maxProperties,omitempty"`
	MinProperties jsontext.Value `json:"minProperties,omitempty"`
	Required      []string       `json:"required,omitempty"`

	Format  string `json:"format,omitempty"`
	Pattern string `json:"pattern,omitempty"`

	// Content vocabulary (spec §8). Annotation-only by default in
	// 2020-12; populated and surfaced in verbose Output but not used
	// to fail validation unless a future content-assertion mode is
	// added.
	ContentMediaType string  `json:"contentMediaType,omitempty"`
	ContentEncoding  string  `json:"contentEncoding,omitempty"`
	ContentSchema    *Schema `json:"contentSchema,omitempty"`

	// Extra captures schema members that don't correspond to any known
	// keyword. JSON Pointers may legitimately reference these (per
	// RFC 6901 + JSON Schema's unknown-keyword behavior); resolver
	// walks fall back to Extra when a normal field lookup misses.
	Extra map[string]jsontext.Value `json:",embed"`
}

// TypeBoolean returns the boolean payload and reports whether m is a
// boolean schema. For object schemas the second return is false.
func (m *Schema) TypeBoolean() (bool, bool) { return m.bool, m.isBool }

// TypeObject returns the [SchemaObject] payload and reports whether
// m is an object schema. For boolean schemas the second return is
// false.
func (m *Schema) TypeObject() (SchemaObject, bool) { return m.object, m.isObject }

// MarshalJSONTo implements the encoding/json/v2 protocol. It emits
// either the boolean payload, the [SchemaObject] body, or an empty
// object — whichever shape m was parsed from.
func (m *Schema) MarshalJSONTo(encoder *jsontext.Encoder) error {
	switch {
	case m.isBool:
		return json.MarshalEncode(encoder, m.bool)
	case m.isObject:
		return json.MarshalEncode(encoder, m.object)
	default:
		return json.MarshalEncode(encoder, SchemaObject{})
	}
}

// UnmarshalJSONFrom implements the encoding/json/v2 protocol.
// Accepts either a boolean (true / false) or an object body and
// records which shape was parsed for [*Schema.TypeBoolean] and
// [*Schema.TypeObject] to dispatch on.
func (m *Schema) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case jsontext.KindFalse, jsontext.KindTrue:
		m.unsetIs()
		m.isBool = true
		return json.UnmarshalDecode(dec, &m.bool)
	case jsontext.KindBeginObject:
		m.unsetIs()
		m.isObject = true
		return json.UnmarshalDecode(dec, &m.object)
	default:
		return errors.New("expected meta to be either a boolean or object")
	}
}
