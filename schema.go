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

type Schema struct {
	isBool, isObject bool
	bool             bool
	object           SchemaObject

	// resolution metadata. zero on freshly-unmarshaled values; populated by Resolve.
	resolved       *Schema
	dynamic        bool
	baseURI        string
	resource       *Schema
	anchors        map[string]*Schema
	dynamicAnchors map[string]*Schema
	source         []byte
	// skipValidation is set on resource roots when their declared
	// metaschema's $vocabulary does not include the JSON Schema
	// validation vocabulary; when true, keywords from that vocabulary
	// (type, enum, const, minimum, …) are treated as no-ops.
	skipValidation bool
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

	// Extra captures schema members that don't correspond to any known
	// keyword. JSON Pointers may legitimately reference these (per
	// RFC 6901 + JSON Schema's unknown-keyword behavior); resolver
	// walks fall back to Extra when a normal field lookup misses.
	Extra map[string]jsontext.Value `json:",inline"`
}

func (m *Schema) TypeBool() (bool, bool)           { return m.bool, m.isBool }
func (m *Schema) TypeObject() (SchemaObject, bool) { return m.object, m.isObject }

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

func (d *Dependency) Required() ([]string, bool) { return d.required, d.required != nil }
func (d *Dependency) Schema() *Schema            { return d.schema }

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

type Type struct {
	isString, isArray bool
	string            TypeString
	array             TypeArray
}

func (t *Type) unsetIs() {
	t.isString = false
	t.isArray = false
}

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

type TypeString = SimpleType

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

func (m *Type) TypeString() (SimpleType, bool)  { return m.string, m.isString }
func (m *Type) TypeArray() ([]SimpleType, bool) { return m.array, m.isArray }

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

type SimpleType string

func (st SimpleType) Validate() error {
	if !slices.Contains(typeEnumStrings(), st) {
		exp, _ := json.Marshal(typeEnumStrings())
		return fmt.Errorf("invalid SimpleType: unexpected enum value %s expected one of %s", string(st), string(exp))
	}
	return nil
}
