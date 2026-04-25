package jsonptr

import (
	"bytes"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// Walker lets a type control how FindValue descends into it. The method
// receives the unconsumed Pointer and returns:
//
//   - rest: the tail the implementation did NOT consume — FindValue
//     continues descending from value with rest. Return "" to indicate
//     "I resolved the whole thing".
//   - value: the Go value reached after consuming the prefix of ptr.
//   - err: any error.
//
// A type may consume one or several tokens per call. Returning the full
// input as rest signals "I don't know how to handle this prefix"; an
// error is the preferred signal.
//
// Walker takes precedence over the json.Marshaler / jsontext.MarshalerTo
// fallback in FindValue, so types that implement Walker preserve Go
// identity across descent rather than round-tripping through JSON bytes.
type Walker interface {
	FindJSONPtrValue(ptr Pointer, opts ...json.Options) (rest Pointer, value any, err error)
}

var walkerType = reflect.TypeFor[Walker]()

// asWalker returns v as a Walker if its type (or its addressable pointer
// type) implements the interface.
func asWalker(v reflect.Value) (Walker, bool) {
	if !v.IsValid() || !v.CanInterface() {
		return nil, false
	}
	t := v.Type()
	if t.Implements(walkerType) {
		return v.Interface().(Walker), true
	}
	if v.CanAddr() && reflect.PointerTo(t).Implements(walkerType) {
		return v.Addr().Interface().(Walker), true
	}
	return nil, false
}

// FindValue navigates to p within in by walking the Go value via reflection
// and returns both the JSON form at that location and the live Go value.
// Any json.Options are forwarded to every Marshal / decoder call the
// implementation makes — both when producing the returned bytes and when
// decoding through a json.Marshaler boundary.
//
// Descent rules, in order of precedence:
//   - If the current value implements Walker, JSONPointerStep is called
//     for the next token. Identity is preserved.
//   - Pointers and interfaces are dereferenced.
//   - Maps with string-kinded keys are looked up by token.
//   - Slices and arrays are indexed by integer token.
//   - Structs are looked up by `json:"name"` tag, falling back to the field
//     name when the tag is absent. Unexported fields are skipped.
//   - When the current value implements json.Marshaler or
//     jsontext.MarshalerTo, the remaining tokens are resolved by marshaling
//     the value and calling Find on the bytes. Identity is lost across
//     this boundary; the second return value is then a freshly decoded
//     value: map[string]any for objects, []any for arrays, *big.Rat for
//     every JSON number (callers convert to int / float64 themselves),
//     and the natural Go counterpart for strings, booleans, and null.
//
// The first return is the JSON encoding at the location (for the in-Go
// path, produced by json.Marshal of the live value).
func FindValue(p Pointer, in any, opts ...json.Options) (jsontext.Value, any, error) {
	if err := p.Validate(); err != nil {
		return nil, nil, err
	}
	cur := reflect.ValueOf(in)
	remaining := p
	for {
		tok, rest, ok := remaining.Head()
		if !ok {
			break
		}
		cur = unwrap(cur)
		if !cur.IsValid() {
			return nil, nil, fmt.Errorf("jsonptr %q: nil at token %q", p, tok)
		}
		if w, walkerOK := asWalker(cur); walkerOK {
			next, child, err := w.FindJSONPtrValue(remaining, opts...)
			if err != nil {
				return nil, nil, fmt.Errorf("jsonptr %q: %w", p, err)
			}
			if len(next) >= len(remaining) {
				return nil, nil, fmt.Errorf("jsonptr %q: walker consumed no tokens at %q", p, tok)
			}
			cur = reflect.ValueOf(child)
			remaining = next
			continue
		}
		if hasCustomJSON(cur) {
			return finishViaJSON(cur, remaining, p, opts)
		}
		next, err := stepValue(cur, tok)
		if err != nil {
			return nil, nil, fmt.Errorf("jsonptr %q: %w", p, err)
		}
		cur = next
		remaining = rest
	}
	var live any
	if cur.IsValid() {
		live = cur.Interface()
	}
	raw, err := json.Marshal(live, opts...)
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
// here to keep the import surface narrow.
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

// finishViaJSON marshals v and resolves the remaining pointer via Find on
// the resulting bytes. The returned Go value is freshly decoded; JSON
// numbers come back as *big.Rat so callers do not lose precision and can
// convert to int / float64 on their own terms.
func finishViaJSON(v reflect.Value, remaining Pointer, p Pointer, opts []json.Options) (jsontext.Value, any, error) {
	raw, err := json.Marshal(v.Interface(), opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("jsonptr %q: marshal: %w", p, err)
	}
	target := jsontext.Value(raw)
	if remaining != "" {
		target, err = Find(raw, remaining, opts...)
		if err != nil {
			return nil, nil, err
		}
	}
	live, err := decodeAny(target, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("jsonptr %q: decode: %w", p, err)
	}
	return target, live, nil
}

// decodeAny decodes a JSON value into a Go any tree, preserving number
// precision by representing every JSON number as *big.Rat. Objects become
// map[string]any, arrays become []any, and strings/bools/null map to their
// natural Go counterparts. Caller-supplied options are forwarded to the
// underlying jsontext.NewDecoder.
//
// json.Options and jsontext.Options are aliases for the same underlying
// jsonopts.Options, so the variadic types are interchangeable.
func decodeAny(data []byte, opts []json.Options) (any, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data), opts...)
	return readAny(dec)
}

func readAny(dec *jsontext.Decoder) (any, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return nil, nil
	case jsontext.KindFalse:
		return false, nil
	case jsontext.KindTrue:
		return true, nil
	case jsontext.KindString:
		return tok.String(), nil
	case jsontext.KindNumber:
		r, ok := new(big.Rat).SetString(tok.String())
		if !ok {
			return nil, fmt.Errorf("invalid number %q", tok.String())
		}
		return r, nil
	case jsontext.KindBeginArray:
		var arr []any
		for dec.PeekKind() != jsontext.KindEndArray {
			v, err := readAny(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return arr, nil
	case jsontext.KindBeginObject:
		m := map[string]any{}
		for dec.PeekKind() != jsontext.KindEndObject {
			key, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			// Capture the key string before any subsequent decoder call
			// voids the token.
			keyStr := key.String()
			v, err := readAny(dec)
			if err != nil {
				return nil, err
			}
			m[keyStr] = v
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return m, nil
	}
	return nil, fmt.Errorf("unexpected token kind %s", tok.Kind())
}
