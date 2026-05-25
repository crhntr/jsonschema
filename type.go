package jsonschema

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

// Type carries the JSON Schema "type" keyword's value, which the
// 2020-12 grammar permits as either a single SimpleType string or a
// non-empty array of them.
//
// Type is hand-rolled rather than generated because the meta-schema
// describes its own "type" field as an anyOf of (simpleTypes,
// array<simpleTypes>) — a shape the generator does not yet collapse
// to a typed Go union, and because the package's accessors return
// the SimpleType named alias instead of a bare string.
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

// TypeString is the single-string form of the type keyword's value.
type TypeString = SimpleType

// TypeArray is the array form of the type keyword's value.
type TypeArray = []SimpleType

func (m *Type) TypeString() (SimpleType, bool)  { return m.string, m.isString }
func (m *Type) TypeArray() ([]SimpleType, bool) { return m.array, m.isArray }

// SimpleType is one of the seven JSON Schema 2020-12 simple types.
type SimpleType string
