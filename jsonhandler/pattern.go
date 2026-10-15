package jsonhandler

import "strings"

// htmlPattern converts a JSON Schema pattern to the value of an HTML
// pattern attribute, or "" when it has no faithful JavaScript form.
//
// Browsers compile pattern with the v flag, which is stricter than the
// regular expressions JSON Schema and Go accept: inside a character class,
// ( ) [ ] { } / | must be escaped, a hyphen that is not part of a range
// must be escaped, and doubled punctuation such as && is reserved. A
// pattern that breaks those rules is not an error in the browser; it is
// silently ignored, so the field would accept anything. Those characters
// are escaped here. Go syntax JavaScript lacks (inline flags, (?P<name>),
// POSIX classes, \A and \z) gives "", leaving the check to the server.
//
// JSON Schema patterns match anywhere in the string, and an HTML pattern
// must match all of it, hence the surrounding .*.
func htmlPattern(p string) string {
	if strings.Contains(p, "[[:") {
		return ""
	}
	var b strings.Builder
	inClass := false
	classStart := 0 // index in b where the class's members begin
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\':
			if i+1 < len(p) && strings.IndexByte("AzZQE", p[i+1]) >= 0 {
				return ""
			}
			b.WriteByte(c)
			if i+1 < len(p) {
				i++
				b.WriteByte(p[i])
			}
		case !inClass && c == '(' && strings.HasPrefix(p[i:], "(?"):
			if !jsGroup(p[i+2:]) {
				return ""
			}
			b.WriteByte(c)
		case !inClass && c == '[':
			inClass = true
			b.WriteByte(c)
			if i+1 < len(p) && p[i+1] == '^' {
				i++
				b.WriteByte('^')
			}
			classStart = b.Len()
			// A ] right after [ or [^ is a literal in Go and an error in v mode.
			if i+1 < len(p) && p[i+1] == ']' {
				i++
				b.WriteString(`\]`)
			}
		case inClass && c == ']':
			inClass = false
			b.WriteByte(c)
		case inClass && c == '-':
			// A range needs something on both sides; otherwise it is a
			// literal hyphen.
			if b.Len() == classStart || i+1 >= len(p) || p[i+1] == ']' {
				b.WriteString(`\-`)
			} else {
				b.WriteByte(c)
			}
		case inClass && strings.IndexByte("(){}/|[", c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		case inClass && i+1 < len(p) && p[i+1] == c && strings.IndexByte("&!#$%*+,.:;<=>?@^`~", c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
		}
	}
	return ".*(?:" + b.String() + ").*"
}

// jsGroup reports whether the text after "(?" starts a group JavaScript
// understands.
func jsGroup(rest string) bool {
	for _, prefix := range []string{":", "=", "!", "<=", "<!"} {
		if strings.HasPrefix(rest, prefix) {
			return true
		}
	}
	// (?<name>…) is a named group in both; Go's (?P<name>…) is not in
	// JavaScript, and neither are inline flags such as (?i).
	return strings.HasPrefix(rest, "<")
}
