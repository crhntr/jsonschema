package jsonptr

import (
	"strconv"
	"strings"
)

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
