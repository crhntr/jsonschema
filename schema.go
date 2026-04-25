package jsonschema

import (
	"errors"
	"fmt"
	"slices"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// Schema is the parsed form of a JSON Schema document or subschema.
type Schema = Meta

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

type Meta struct {
	isBool, isObject bool
	bool             bool
	object           MetaObject

	// resolution metadata. zero on freshly-unmarshaled values; populated by Resolve.
	resolved       *Meta
	dynamic        bool
	baseURI        string
	resource       *Meta
	anchors        map[string]*Meta
	dynamicAnchors map[string]*Meta
	source         []byte
}

// Resolved returns the lexical-scope target of $ref or $dynamicRef, or nil if
// this subschema has no reference or the schema has not been resolved.
func (m *Meta) Resolved() *Meta { return m.resolved }

// IsDynamic reports whether this subschema's $dynamicRef is "bookended" per
// JSON Schema 2020-12 §8.2.3.2 — i.e., the initial lexical target's resource
// contains a matching $dynamicAnchor, so a validator must walk the dynamic
// scope at validation time.
func (m *Meta) IsDynamic() bool { return m.dynamic }

// BaseURI returns the lexical-scope base URI in effect at this subschema.
func (m *Meta) BaseURI() string { return m.baseURI }

// Resource returns the root of the JSON Schema resource (the nearest
// enclosing schema that defines $id, or the document root) containing this
// subschema. Returns nil before resolution.
func (m *Meta) Resource() *Meta { return m.resource }

// Anchor looks up a $anchor by name within this resource. Only meaningful
// when called on a resource root (m == m.Resource()).
func (m *Meta) Anchor(name string) *Meta { return m.anchors[name] }

// DynamicAnchor looks up a $dynamicAnchor by name within this resource.
func (m *Meta) DynamicAnchor(name string) *Meta { return m.dynamicAnchors[name] }

// Source returns the original JSON document bytes this Meta was parsed from.
// Only populated on resource roots (top-level document or embedded $id
// resources within the same document share the same slice). Returns nil
// otherwise.
func (m *Meta) Source() []byte { return m.source }

func (m *Meta) unsetIs() {
	m.isBool = false
	m.isObject = false
}

type MetaObject struct {
	ID     string `json:"$id,omitempty"`
	Schema string `json:"$schema,omitempty"`

	Ref           string `json:"$ref,omitempty"`
	Anchor        string `json:"$anchor,omitempty"`
	DynamicRef    string `json:"$dynamicRef,omitempty"`
	DynamicAnchor string `json:"$dynamicAnchor,omitempty"`

	Vocabulary map[string]bool `json:"$vocabulary,omitempty"`

	Comment string `json:"$comment,omitempty"`

	Defs map[string]*Meta `json:"$defs,omitempty"`

	If   *Meta `json:"if,omitempty"`
	Then *Meta `json:"then,omitempty"`
	Else *Meta `json:"else,omitempty"`

	AllOf []Meta `json:"allOf,omitempty"`
	AnyOf []Meta `json:"anyOf,omitempty"`
	OneOf []Meta `json:"oneOf,omitempty"`
	Not   *Meta  `json:"not,omitempty"`

	Properties           map[string]*Meta `json:"properties,omitempty"`
	AdditionalProperties *Meta           `json:"additionalProperties,omitempty"`
	PropertyNames        *Meta           `json:"propertyNames,omitempty"`

	// meta-data.json
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Deprecated  bool     `json:"deprecated,omitempty"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
	WriteOnly   bool     `json:"writeOnly,omitempty"`
	Examples    []string `json:"examples,omitempty"`

	Items *Meta `json:"items,omitempty"`

	Type    *Type            `json:"type,omitempty"`
	Enum    []jsontext.Value `json:"enum,omitempty"`
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

	Format  string `json:"format,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

func NewMetaTypeBool(in bool) *Meta {
	return &Meta{isBool: true, bool: in}
}

func NewMetaTypeObject(in MetaObject) *Meta {
	return &Meta{isObject: true, object: in}
}

func (m *Meta) TypeBool() (bool, bool)         { return m.bool, m.isBool }
func (m *Meta) TypeObject() (MetaObject, bool) { return m.object, m.isObject }

func (m *Meta) MarshalJSONTo(encoder *jsontext.Encoder) error {
	switch {
	case m.isBool:
		return json.MarshalEncode(encoder, m.bool)
	case m.isObject:
		return json.MarshalEncode(encoder, m.object)
	default:
		return json.MarshalEncode(encoder, MetaObject{})
	}
}

func (m *Meta) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
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

func NewTypeTypeString(in SimpleType) *Type {
	return &Type{isString: true, string: in}
}

func NewTypeTypeArray(in []SimpleType) *Type {
	return &Type{isArray: true, array: in}
}

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
