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
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
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

// Schema is a parsed JSON Schema document or subschema. A Schema
// holds either a boolean schema (true / false) or an object schema
// ([SchemaObject]); use [*Schema.TypeBool] and [*Schema.TypeObject]
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

// PathInResource returns the RFC 6901 JSON Pointer from this subschema's
// resource root to itself. Empty for the resource root; reset to empty
// when an embedded $id opens a new resource. Used to construct
// absoluteKeywordLocation values for spec-compliant validation output.
func (m *Schema) PathInResource() string { return m.pathInResource }

// Resource returns the root of the JSON Schema resource (the nearest
// enclosing schema that defines $id, or the document root) containing this
// subschema. Returns nil before resolution.
func (m *Schema) Resource() *Schema { return m.resource }

// Anchor looks up a $anchor by name within this resource. Only meaningful
// when called on a resource root (m == m.Resource()).
func (m *Schema) Anchor(name string) *Schema { return m.anchors[name] }

// DynamicAnchor looks up a $dynamicAnchor by name within this resource.
func (m *Schema) DynamicAnchor(name string) *Schema { return m.dynamicAnchors[name] }

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

// Source returns the original JSON document bytes this Schema was parsed from.
// Only populated on resource roots (top-level document or embedded $id
// resources within the same document share the same slice). Returns nil
// otherwise.
func (m *Schema) Source() []byte { return m.source }

func (m *Schema) unsetIs() {
	m.isBool = false
	m.isObject = false
}

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
	Extra map[string]jsontext.Value `json:",inline"`
}

// TypeBool returns the boolean payload and reports whether m is a
// boolean schema. For object schemas the second return is false.
func (m *Schema) TypeBool() (bool, bool) { return m.bool, m.isBool }

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
// records which shape was parsed for [*Schema.TypeBool] and
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

// Dependency represents a single entry of the legacy dependencies
// keyword: either a list of required properties or a subschema.
type Dependency struct {
	required []string
	schema   *Schema
}

// Required returns the list of required property names and reports
// whether d was parsed from the array form of dependencies. The
// schema form returns ok=false.
func (d *Dependency) Required() ([]string, bool) { return d.required, d.required != nil }

// Schema returns the subschema and is non-nil only when d was parsed
// from the schema form of dependencies.
func (d *Dependency) Schema() *Schema { return d.schema }

// UnmarshalJSONFrom implements the encoding/json/v2 protocol.
// Accepts either an array of property names (legacy required form)
// or a subschema body.
func (d *Dependency) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case jsontext.KindBeginArray:
		var req []string
		if err := json.UnmarshalDecode(dec, &req); err != nil {
			return err
		}
		d.required = req
		return nil
	default:
		var m Schema
		if err := json.UnmarshalDecode(dec, &m); err != nil {
			return err
		}
		d.schema = &m
		return nil
	}
}

// Type is the value of the JSON Schema "type" keyword. Per the spec
// it may be a single type name (e.g. "string") or an array of names
// (e.g. ["string","null"]); the two shapes round-trip through
// [Type.TypeString] and [Type.TypeArray] respectively.
type Type struct {
	isString, isArray bool
	string            TypeString
	array             TypeArray
}

func (t *Type) unsetIs() {
	t.isString = false
	t.isArray = false
}

// MarshalJSONTo implements the encoding/json/v2 protocol. Emits
// either the single-string form or the array form, depending on how
// t was parsed; an unset Type marshals as null.
func (t *Type) MarshalJSONTo(enc *jsontext.Encoder) error {
	switch {
	case t.isString:
		return json.MarshalEncode(enc, t.string)
	case t.isArray:
		return json.MarshalEncode(enc, t.array)
	default:
		return enc.WriteToken(jsontext.Null)
	}
}

// UnmarshalJSONFrom implements the encoding/json/v2 protocol.
// Accepts either a single string ("string", "object", …) or an array
// of such strings.
func (t *Type) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case jsontext.KindBeginArray:
		t.unsetIs()
		t.isArray = true
		return json.UnmarshalDecode(dec, &t.array)
	case jsontext.KindString:
		t.unsetIs()
		t.isString = true
		return json.UnmarshalDecode(dec, &t.string)
	default:
		return errors.New("expected type to be either a string or array of strings")
	}
}

// TypeString is the single-string form of the "type" keyword, an
// alias for [SimpleType] so that callers branching on Type's two
// shapes can be explicit about which they expect.
type TypeString = SimpleType

// TypeArray is the array form of the "type" keyword: an ordered list
// of [SimpleType] values. The instance must match at least one.
type TypeArray = []SimpleType

func typeEnumStrings() []SimpleType {
	return []SimpleType{
		"array",
		"boolean",
		"integer",
		"null",
		"number",
		"object",
		"string",
	}
}

// TypeString returns the single-string payload and reports whether
// m was parsed from a single type name.
func (m *Type) TypeString() (SimpleType, bool) { return m.string, m.isString }

// TypeArray returns the array payload and reports whether m was
// parsed from an array of type names.
func (m *Type) TypeArray() ([]SimpleType, bool) { return m.array, m.isArray }

// Validate reports whether m's contents are valid JSON Schema type
// values: each entry must be one of the seven SimpleType strings
// defined by the spec, and an array form must be non-empty.
func (m *Type) Validate() error {
	if m.isArray {
		for _, item := range m.array {
			if err := item.Validate(); err != nil {
				return err
			}
		}
		if len(m.array) < 1 {
			return errors.New("type array must have at least one item")
		}
	} else if m.isString {
		return m.string.Validate()
	}
	return nil
}

// SimpleType is one of the seven JSON Schema primitive type names:
// "array", "boolean", "integer", "null", "number", "object",
// "string". Use [SimpleType.Validate] to confirm a value is in that
// closed set.
type SimpleType string

// Validate reports whether st is one of the seven JSON Schema
// primitive type names.
func (st SimpleType) Validate() error {
	if !slices.Contains(typeEnumStrings(), st) {
		exp, _ := json.Marshal(typeEnumStrings())
		return fmt.Errorf("invalid SimpleType: unexpected enum value %s expected one of %s", string(st), string(exp))
	}
	return nil
}
