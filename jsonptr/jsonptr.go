// Package jsonptr implements RFC 6901 JSON Pointers over raw JSON bytes.
//
// A Pointer is a slash-separated sequence of reference tokens that
// identifies a location within a JSON document. The empty pointer "" refers
// to the root value; "/foo" refers to the value of object member "foo";
// "/0" refers to the first element of an array; tokens use "~1" to encode
// "/" and "~0" to encode "~".
package jsonptr

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/go-json-experiment/json/jsontext"
)

// Pointer is an RFC 6901 JSON Pointer in string form.
//
// The zero value, "", refers to the root of any JSON document. A non-empty
// Pointer must begin with "/" and is a sequence of "/"-separated reference
// tokens. Tokens are unescaped per RFC 6901 §4: "~1" -> "/", "~0" -> "~"
// (decoded in that order).
type Pointer string

// Tokens splits p into its sequence of reference tokens, unescaped.
// Returns nil for the root pointer.
func (p Pointer) Tokens() []string {
	if p == "" {
		return nil
	}
	raw := strings.Split(string(p)[1:], "/")
	out := make([]string, len(raw))
	for i, t := range raw {
		out[i] = unescapeToken(t)
	}
	return out
}

// Append returns a new Pointer that descends through token. The token is
// escaped per RFC 6901.
func (p Pointer) Append(token string) Pointer {
	return p + Pointer("/"+escapeToken(token))
}

// IsRoot reports whether p is the root pointer ("").
func (p Pointer) IsRoot() bool { return p == "" }

// Validate reports an error if p is not a syntactically valid RFC 6901
// pointer. The empty pointer and any string starting with "/" are valid.
func (p Pointer) Validate() error {
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(string(p), "/") {
		return fmt.Errorf("jsonptr: %q does not begin with %q", p, "/")
	}
	return nil
}

// Find returns the JSON value located at p within data. The returned value
// preserves the original byte representation (whitespace, number form,
// member order). Non-existent members, missing indices, and pointers that
// descend into a scalar all return errors.
func Find(data []byte, p Pointer) (jsontext.Value, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data))
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

func escapeToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

func unescapeToken(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}
