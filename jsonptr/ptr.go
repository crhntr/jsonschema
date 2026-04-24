// Package jsonptr implements RFC 6901 JSON Pointers over raw JSON bytes
// and arbitrary Go values.
//
// A Pointer is a slash-separated sequence of reference tokens that
// identifies a location within a JSON document. The empty pointer "" refers
// to the root value; "/foo" refers to the value of object member "foo";
// "/0" refers to the first element of an array; tokens use "~1" to encode
// "/" and "~0" to encode "~".
package jsonptr

import (
	"fmt"
	"strings"
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
