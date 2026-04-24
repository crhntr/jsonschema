package jsonschema

import (
	"bytes"
	"errors"
	"io"

	"github.com/go-json-experiment/json/jsontext"
	"github.com/go-json-experiment/json/v1"
)

func (m *Meta) Evaluate(name string, in []byte) error {
	if !json.Valid(in) {
		return NewErrorWithPosition(name, in, 0, errors.New("invalid JSON"))
	}
	dec := jsontext.NewDecoder(bytes.NewReader(in))

	if o, ok := m.TypeObject(); ok {
		return o.validate(name, in, dec)
	}

	if b, ok := m.TypeBool(); ok {
		return validateMetaTypeBool(name, in, dec, b)
	}

	return nil
}

func validateMetaTypeBool(name string, in []byte, dec *jsontext.Decoder, b bool) error {
	if b {
		if err := dec.SkipValue(); err != nil {
			return NewErrorWithPosition(name, in, dec.InputOffset(), err)
		}
		return nil
	}
	if _, err := dec.ReadValue(); err != nil {
		if !errors.Is(err, io.EOF) {
			return NewErrorWithPosition(name, in, dec.InputOffset(), err)
		}
	}
	return NewErrorWithPosition(name, in, dec.InputOffset(), errors.New("nothing allowed here"))
}

func (o *MetaObject) validate(name string, in []byte, dec *jsontext.Decoder) error {

	if err := dec.SkipValue(); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return NewErrorWithPosition(name, in, dec.InputOffset(), err)
	}

	return nil
}
