package jsonptr

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	"github.com/go-json-experiment/json/jsontext"
)

// Find returns the JSON value located at p within data. The returned value
// preserves the original byte representation (whitespace, number form,
// member order). Non-existent members, missing indices, and pointers that
// descend into a scalar all return errors. Caller-supplied options are
// forwarded to jsontext.NewDecoder.
func Find(data []byte, p Pointer, opts ...jsontext.Options) (jsontext.Value, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data), opts...)
	if err := descend(dec, p.Tokens(), p); err != nil {
		return nil, err
	}
	v, err := dec.ReadValue()
	if err != nil {
		return nil, fmt.Errorf("jsonptr %q: read value: %w", p, err)
	}
	return v, nil
}

// descend positions dec at the start of the value identified by tokens.
// On return, the next ReadValue / ReadToken call sees that value.
func descend(dec *jsontext.Decoder, tokens []string, p Pointer) error {
	for _, tok := range tokens {
		switch dec.PeekKind() {
		case jsontext.KindBeginObject:
			if err := descendObject(dec, tok, p); err != nil {
				return err
			}
		case jsontext.KindBeginArray:
			if err := descendArray(dec, tok, p); err != nil {
				return err
			}
		default:
			return fmt.Errorf("jsonptr %q: cannot descend into %s at %q",
				p, dec.PeekKind(), tok)
		}
	}
	return nil
}

func descendObject(dec *jsontext.Decoder, target string, p Pointer) error {
	if _, err := dec.ReadToken(); err != nil { // consume '{'
		return err
	}
	for {
		if dec.PeekKind() == jsontext.KindEndObject {
			return fmt.Errorf("jsonptr %q: missing object member %q", p, target)
		}
		key, err := dec.ReadToken()
		if err != nil {
			return err
		}
		if key.String() == target {
			return nil
		}
		if _, err := dec.ReadValue(); err != nil {
			return err
		}
	}
}

func descendArray(dec *jsontext.Decoder, target string, p Pointer) error {
	idx, err := strconv.Atoi(target)
	if err != nil {
		return fmt.Errorf("jsonptr %q: token %q is not a valid array index", p, target)
	}
	if idx < 0 {
		return fmt.Errorf("jsonptr %q: array index %d is negative", p, idx)
	}
	if _, err := dec.ReadToken(); err != nil { // consume '['
		return err
	}
	for i := 0; ; i++ {
		if dec.PeekKind() == jsontext.KindEndArray {
			return fmt.Errorf("jsonptr %q: array index %d out of range", p, idx)
		}
		if i == idx {
			return nil
		}
		if _, err := dec.ReadValue(); err != nil {
			if err == io.EOF {
				return fmt.Errorf("jsonptr %q: unexpected EOF", p)
			}
			return err
		}
	}
}
