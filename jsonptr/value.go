package jsonptr

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

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
