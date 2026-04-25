package jsonschema

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"

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
	off := dec.InputOffset()
	val, err := dec.ReadValue()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return NewErrorWithPosition(name, in, dec.InputOffset(), err)
	}
	return o.validateValue(name, in, off, val)
}

func (o *MetaObject) validateValue(name string, in []byte, off int64, val jsontext.Value) error {
	kind := jsontext.NewDecoder(bytes.NewReader(val)).PeekKind()
	if o.Type != nil {
		if err := o.validateType(name, in, off, kind, val); err != nil {
			return err
		}
	}
	if o.Enum != nil {
		if err := validateEnum(o.Enum, val); err != nil {
			return NewErrorWithPosition(name, in, off, err)
		}
	}
	if len(o.Const) > 0 {
		eq, err := Equal(val, o.Const)
		if err != nil {
			return NewErrorWithPosition(name, in, off, err)
		}
		if !eq {
			return NewErrorWithPosition(name, in, off, fmt.Errorf("value does not equal const"))
		}
	}
	switch kind {
	case jsontext.KindNumber:
		if err := o.validateNumber(val); err != nil {
			return NewErrorWithPosition(name, in, off, err)
		}
	case jsontext.KindString:
		if err := o.validateString(val); err != nil {
			return NewErrorWithPosition(name, in, off, err)
		}
	case jsontext.KindBeginObject:
		if err := o.validateObject(name, in, off, val); err != nil {
			return err
		}
	case jsontext.KindBeginArray:
		if err := o.validateArray(name, in, off, val); err != nil {
			return err
		}
	}
	if err := o.validateComposition(name, in, off, val); err != nil {
		return err
	}
	return nil
}

func (o *MetaObject) validateComposition(name string, in []byte, off int64, val jsontext.Value) error {
	for i := range o.AllOf {
		if err := o.AllOf[i].Evaluate(fmt.Sprintf("%s/allOf/%d", name, i), val); err != nil {
			return err
		}
	}
	if len(o.AnyOf) > 0 {
		matched := false
		for i := range o.AnyOf {
			if o.AnyOf[i].Evaluate(fmt.Sprintf("%s/anyOf/%d", name, i), val) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return NewErrorWithPosition(name, in, off, fmt.Errorf("anyOf: no subschema matched"))
		}
	}
	if len(o.OneOf) > 0 {
		matches := 0
		for i := range o.OneOf {
			if o.OneOf[i].Evaluate(fmt.Sprintf("%s/oneOf/%d", name, i), val) == nil {
				matches++
			}
		}
		if matches != 1 {
			return NewErrorWithPosition(name, in, off,
				fmt.Errorf("oneOf: %d subschemas matched, want exactly 1", matches))
		}
	}
	if o.Not != nil {
		if o.Not.Evaluate(name+"/not", val) == nil {
			return NewErrorWithPosition(name, in, off, fmt.Errorf("not: subschema unexpectedly matched"))
		}
	}
	if o.If != nil {
		ifErr := o.If.Evaluate(name+"/if", val)
		if ifErr == nil && o.Then != nil {
			if err := o.Then.Evaluate(name+"/then", val); err != nil {
				return err
			}
		}
		if ifErr != nil && o.Else != nil {
			if err := o.Else.Evaluate(name+"/else", val); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *MetaObject) validateObject(name string, in []byte, off int64, val jsontext.Value) error {
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		return NewErrorWithPosition(name, in, off, err)
	}
	keys := map[string]struct{}{}
	count := 0
	for dec.PeekKind() != jsontext.KindEndObject {
		keyTok, err := dec.ReadToken()
		if err != nil {
			return NewErrorWithPosition(name, in, off, err)
		}
		key := keyTok.String()
		keys[key] = struct{}{}
		count++
		propVal, err := dec.ReadValue()
		if err != nil {
			return NewErrorWithPosition(name, in, off, err)
		}
		if sub, ok := o.Properties[key]; ok && sub != nil {
			if err := sub.Evaluate(name+"."+key, propVal); err != nil {
				return err
			}
		}
	}
	for _, req := range o.Required {
		if _, ok := keys[req]; !ok {
			return NewErrorWithPosition(name, in, off,
				fmt.Errorf("missing required property %q", req))
		}
	}
	if min, ok := parseInt(o.MinProperties); ok && count < min {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("object has %d properties, minProperties %d", count, min))
	}
	if max, ok := parseInt(o.MaxProperties); ok && count > max {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("object has %d properties, maxProperties %d", count, max))
	}
	return nil
}

func (o *MetaObject) validateArray(name string, in []byte, off int64, val jsontext.Value) error {
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		return NewErrorWithPosition(name, in, off, err)
	}
	var items []jsontext.Value
	for dec.PeekKind() != jsontext.KindEndArray {
		v, err := dec.ReadValue()
		if err != nil {
			return NewErrorWithPosition(name, in, off, err)
		}
		items = append(items, v)
	}
	if min, ok := parseInt(o.MinItems); ok && len(items) < min {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("array has %d items, minItems %d", len(items), min))
	}
	if max, ok := parseInt(o.MaxItems); ok && len(items) > max {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("array has %d items, maxItems %d", len(items), max))
	}
	if o.UniqueItems {
		for i := range items {
			for j := i + 1; j < len(items); j++ {
				eq, err := Equal(items[i], items[j])
				if err != nil {
					return NewErrorWithPosition(name, in, off, err)
				}
				if eq {
					return NewErrorWithPosition(name, in, off,
						fmt.Errorf("array items %d and %d are equal", i, j))
				}
			}
		}
	}
	if o.Items != nil {
		for i, item := range items {
			if err := o.Items.Evaluate(fmt.Sprintf("%s[%d]", name, i), item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *MetaObject) validateNumber(val jsontext.Value) error {
	n, ok := new(big.Rat).SetString(string(val))
	if !ok {
		return fmt.Errorf("invalid number %s", val)
	}
	if cmp, ok := compareRat(o.Minimum, n); ok && cmp > 0 {
		return fmt.Errorf("less than minimum %s", o.Minimum)
	}
	if cmp, ok := compareRat(o.Maximum, n); ok && cmp < 0 {
		return fmt.Errorf("greater than maximum %s", o.Maximum)
	}
	if cmp, ok := compareRat(o.ExclusiveMinimum, n); ok && cmp >= 0 {
		return fmt.Errorf("not strictly greater than exclusiveMinimum %s", o.ExclusiveMinimum)
	}
	if cmp, ok := compareRat(o.ExclusiveMaximum, n); ok && cmp <= 0 {
		return fmt.Errorf("not strictly less than exclusiveMaximum %s", o.ExclusiveMaximum)
	}
	if len(o.MultipleOf) > 0 {
		div, ok := new(big.Rat).SetString(string(o.MultipleOf))
		if !ok || div.Sign() == 0 {
			return fmt.Errorf("invalid multipleOf %s", o.MultipleOf)
		}
		quot := new(big.Rat).Quo(n, div)
		if !quot.IsInt() {
			return fmt.Errorf("not a multiple of %s", o.MultipleOf)
		}
	}
	return nil
}

// compareRat parses keyword as a big.Rat and reports keyword.Cmp(n).
// ok is false when keyword is empty (i.e. not present in the schema).
func compareRat(keyword jsontext.Value, n *big.Rat) (int, bool) {
	if len(keyword) == 0 {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(string(keyword))
	if !ok {
		return 0, false
	}
	return r.Cmp(n), true
}

func (o *MetaObject) validateString(val jsontext.Value) error {
	if len(o.MinLength) == 0 && len(o.MaxLength) == 0 && o.Pattern == "" {
		return nil
	}
	s, err := decodeJSONString(val)
	if err != nil {
		return err
	}
	count := utf8.RuneCountInString(s)
	if min, ok := parseInt(o.MinLength); ok && count < min {
		return fmt.Errorf("string length %d less than minLength %d", count, min)
	}
	if max, ok := parseInt(o.MaxLength); ok && count > max {
		return fmt.Errorf("string length %d greater than maxLength %d", count, max)
	}
	if o.Pattern != "" {
		re, err := regexp.Compile(o.Pattern)
		if err != nil {
			return fmt.Errorf("invalid pattern %q: %w", o.Pattern, err)
		}
		if !re.MatchString(s) {
			return fmt.Errorf("string does not match pattern %q", o.Pattern)
		}
	}
	return nil
}

// decodeJSONString returns the Go string represented by a JSON-encoded
// string value (i.e. the bytes inside the quotes, with escapes
// interpreted).
func decodeJSONString(val jsontext.Value) (string, error) {
	var s string
	if err := json.Unmarshal(val, &s); err != nil {
		return "", err
	}
	return s, nil
}

func parseInt(v jsontext.Value) (int, bool) {
	if len(v) == 0 {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(string(v))
	if !ok || !r.IsInt() {
		return 0, false
	}
	return int(r.Num().Int64()), true
}

func (o *MetaObject) validateType(name string, in jsontext.Value, off int64, kind jsontext.Kind, val jsontext.Value) error {
	for _, t := range typeNames(o.Type) {
		if matchesType(t, kind, val) {
			return nil
		}
	}
	return NewErrorWithPosition(name, in, off,
		fmt.Errorf("type %s does not match %s", typeListString(o.Type), kind))
}

func validateEnum(enum []jsontext.Value, val jsontext.Value) error {
	for _, e := range enum {
		eq, err := Equal(val, e)
		if err != nil {
			return err
		}
		if eq {
			return nil
		}
	}
	return fmt.Errorf("value is not in enum")
}

func typeNames(t *Type) []string {
	if t == nil {
		return nil
	}
	if s, ok := t.TypeString(); ok {
		return []string{string(s)}
	}
	if a, ok := t.TypeArray(); ok {
		out := make([]string, len(a))
		for i, v := range a {
			out[i] = string(v)
		}
		return out
	}
	return nil
}

func typeListString(t *Type) string {
	return strings.Join(typeNames(t), "|")
}

func matchesType(t string, kind jsontext.Kind, val jsontext.Value) bool {
	switch t {
	case "string":
		return kind == jsontext.KindString
	case "number":
		return kind == jsontext.KindNumber
	case "integer":
		return kind == jsontext.KindNumber && numberIsInteger(val)
	case "boolean":
		return kind == jsontext.KindTrue || kind == jsontext.KindFalse
	case "null":
		return kind == jsontext.KindNull
	case "object":
		return kind == jsontext.KindBeginObject
	case "array":
		return kind == jsontext.KindBeginArray
	}
	return false
}

// numberIsInteger reports whether the JSON number bytes represent an
// integer-valued number (no fractional part, or a fractional part that
// is all zeros).
func numberIsInteger(val jsontext.Value) bool {
	val = bytes.TrimSpace(val)
	dot := bytes.IndexByte(val, '.')
	if dot < 0 {
		return true
	}
	end := len(val)
	if e := bytes.IndexAny(val, "eE"); e >= 0 {
		end = e
	}
	for _, c := range val[dot+1 : end] {
		if c != '0' {
			return false
		}
	}
	return true
}
