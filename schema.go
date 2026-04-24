package jsonschema

import (
	"errors"
	"fmt"
	"slices"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

type Meta struct {
	isBool, isObject bool
	bool             bool
	object           MetaObject
}

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

	Defs map[string]Meta `json:"$defs,omitempty"`

	If   *Meta `json:"if,omitempty"`
	Then *Meta `json:"then,omitempty"`
	Else *Meta `json:"else,omitempty"`

	AllOf []Meta `json:"allOf,omitempty"`
	AnyOf []Meta `json:"anyOf,omitempty"`
	OneOf []Meta `json:"oneOf,omitempty"`
	Not   *Meta  `json:"not,omitempty"`

	Properties           map[string]Meta `json:"properties,omitempty"`
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
