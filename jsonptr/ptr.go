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
	"iter"
	"strconv"
	"strings"
)

// Pointer is an RFC 6901 JSON Pointer in string form.
//
// The zero value, "", refers to the root of any JSON document. A non-empty
// Pointer must begin with "/" and is a sequence of "/"-separated reference
// tokens. Tokens are unescaped per RFC 6901 §4: "~1" -> "/", "~0" -> "~"
// (decoded in that order).
type Pointer string

// Tokens yields the sequence of reference tokens that make up p,
// unescaped per RFC 6901. The root pointer yields nothing.
func (p Pointer) Tokens() iter.Seq[string] {
	return func(yield func(string) bool) {
		cur := p
		for {
			tok, rest, ok := cur.Head()
			if !ok {
				return
			}
			if !yield(tok) {
				return
			}
			cur = rest
		}
	}
}

// Append returns a new Pointer that descends through token. The token is
// escaped per RFC 6901.
func (p Pointer) Append(token string) Pointer {
	return p + Pointer("/"+escapeToken(token))
}

// IsRoot reports whether p is the root pointer ("").
func (p Pointer) IsRoot() bool { return p == "" }

// Head splits p into its first token and the remaining Pointer. ok is
// false when p is the root pointer.
func (p Pointer) Head() (token string, rest Pointer, ok bool) {
	if p == "" {
		return "", "", false
	}
	body := string(p)[1:]
	if i := strings.IndexByte(body, '/'); i >= 0 {
		return unescapeToken(body[:i]), Pointer(body[i:]), true
	}
	return unescapeToken(body), "", true
}

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

// Builder constructs a Pointer by appending tokens to an underlying
// strings.Builder. The zero value is ready to use and produces "" until
// any token is appended; pass an existing prefix via NewBuilder.
type Builder struct {
	sb strings.Builder
}

// NewBuilder returns a Builder seeded with the given prefix (typically a
// parent Pointer or a free-form path used in error context).
func NewBuilder(prefix string) *Builder {
	var b Builder
	b.sb.WriteString(prefix)
	return &b
}

// Token appends an RFC 6901 reference token. The token is escaped.
func (b *Builder) Token(token string) *Builder {
	b.sb.WriteByte('/')
	b.sb.WriteString(escapeToken(token))
	return b
}

// Index appends a non-negative integer token (no escaping needed).
func (b *Builder) Index(i int) *Builder {
	b.sb.WriteByte('/')
	b.sb.WriteString(strconv.Itoa(i))
	return b
}

// Raw appends raw bytes to the builder without escaping or a leading
// slash. Use sparingly — it bypasses RFC 6901 escaping.
func (b *Builder) Raw(s string) *Builder {
	b.sb.WriteString(s)
	return b
}

// Pointer returns the Pointer built so far.
func (b *Builder) Pointer() Pointer { return Pointer(b.sb.String()) }

// String returns the built path as a plain string.
func (b *Builder) String() string { return b.sb.String() }

// Reset discards all accumulated content.
func (b *Builder) Reset() { b.sb.Reset() }

func unescapeToken(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}
