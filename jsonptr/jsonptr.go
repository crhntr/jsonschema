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
	"reflect"
	"strconv"
	"strings"

	"github.com/go-json-experiment/json"
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

// FindValue navigates to p within in by walking the Go value via reflection
// and returns both the JSON form at that location and the live Go value.
//
// Reflection rules:
//   - Pointers and interfaces are dereferenced.
//   - Maps with string-kinded keys are looked up by token.
//   - Slices and arrays are indexed by integer token.
//   - Structs are looked up by `json:"name"` tag, falling back to the field
//     name when the tag is absent. Unexported fields are skipped.
//   - When the current value implements json.Marshaler or
//     jsontext.MarshalerTo, the remaining tokens are resolved by marshaling
//     the value and calling Find on the bytes. Identity is lost across
//     this boundary; the second return value is then a freshly decoded
//     value (typically map[string]any / []any / scalar).
//
// The first return is the JSON encoding at the location (for the in-Go
// path, produced by json.Marshal of the live value).
func FindValue(p Pointer, in any) (jsontext.Value, any, error) {
	if err := p.Validate(); err != nil {
		return nil, nil, err
	}
	cur := reflect.ValueOf(in)
	tokens := p.Tokens()
	for i, tok := range tokens {
		cur = unwrap(cur)
		if !cur.IsValid() {
			return nil, nil, fmt.Errorf("jsonptr %q: nil at token %q", p, tok)
		}
		if hasCustomJSON(cur) {
			return finishViaJSON(cur, tokens[i:], p)
		}
		next, err := stepValue(cur, tok)
		if err != nil {
			return nil, nil, fmt.Errorf("jsonptr %q: %w", p, err)
		}
		cur = next
	}
	cur = unwrap(cur)
	var live any
	if cur.IsValid() {
		live = cur.Interface()
	}
	raw, err := json.Marshal(live)
	if err != nil {
		return nil, nil, fmt.Errorf("jsonptr %q: marshal final: %w", p, err)
	}
	return raw, live, nil
}

func unwrap(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

var (
	jsonMarshalerType   = reflect.TypeFor[json.Marshaler]()
	jsonMarshalerToType = reflect.TypeFor[jsontextMarshalerTo]()
)

// jsontextMarshalerTo mirrors the v2 streaming marshal interface; declared
// here to keep Find / FindValue's import surface narrow.
type jsontextMarshalerTo interface {
	MarshalJSONTo(*jsontext.Encoder) error
}

func hasCustomJSON(v reflect.Value) bool {
	t := v.Type()
	if t.Implements(jsonMarshalerType) || t.Implements(jsonMarshalerToType) {
		return true
	}
	if v.CanAddr() {
		pt := reflect.PointerTo(t)
		if pt.Implements(jsonMarshalerType) || pt.Implements(jsonMarshalerToType) {
			return true
		}
	}
	return false
}

func stepValue(v reflect.Value, tok string) (reflect.Value, error) {
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return reflect.Value{}, fmt.Errorf("map keyed by %s, not string", v.Type().Key())
		}
		key := reflect.New(v.Type().Key()).Elem()
		key.SetString(tok)
		got := v.MapIndex(key)
		if !got.IsValid() {
			return reflect.Value{}, fmt.Errorf("missing object member %q", tok)
		}
		return got, nil
	case reflect.Slice, reflect.Array:
		idx, err := strconv.Atoi(tok)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("token %q is not a valid array index", tok)
		}
		if idx < 0 || idx >= v.Len() {
			return reflect.Value{}, fmt.Errorf("array index %d out of range [0,%d)", idx, v.Len())
		}
		return v.Index(idx), nil
	case reflect.Struct:
		f, ok := lookupStructField(v.Type(), tok)
		if !ok {
			return reflect.Value{}, fmt.Errorf("no struct field for %q", tok)
		}
		return v.FieldByIndex(f.Index), nil
	default:
		return reflect.Value{}, fmt.Errorf("cannot descend into %s at token %q", v.Kind(), tok)
	}
}

func lookupStructField(t reflect.Type, name string) (reflect.StructField, bool) {
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		if jsonFieldName(f) == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	if tag == "" {
		return f.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return f.Name
	}
	return name
}

// finishViaJSON marshals v and resolves the remaining tokens via Find on
// the resulting bytes. The returned Go value is freshly decoded.
func finishViaJSON(v reflect.Value, remaining []string, p Pointer) (jsontext.Value, any, error) {
	raw, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, nil, fmt.Errorf("jsonptr %q: marshal: %w", p, err)
	}
	if len(remaining) == 0 {
		var live any
		if err := json.Unmarshal(raw, &live); err != nil {
			return nil, nil, fmt.Errorf("jsonptr %q: unmarshal: %w", p, err)
		}
		return raw, live, nil
	}
	var sub Pointer
	for _, tok := range remaining {
		sub = sub.Append(tok)
	}
	found, err := Find(raw, sub)
	if err != nil {
		return nil, nil, err
	}
	var live any
	if err := json.Unmarshal(found, &live); err != nil {
		return nil, nil, fmt.Errorf("jsonptr %q: unmarshal: %w", p, err)
	}
	return found, live, nil
}
