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

	"github.com/crhntr/jsonschema/jsonptr"
)

// childPath returns parent/token/index — used to build per-keyword paths
// for error context without going through fmt.Sprintf.
func childPath(parent, keyword string, index int) string {
	return jsonptr.NewBuilder(parent).Token(keyword).Index(index).String()
}

// childKey returns parent/key (key is escaped per RFC 6901).
func childKey(parent, key string) string {
	return jsonptr.NewBuilder(parent).Token(key).String()
}

// childKeyword returns parent/keyword (no escaping needed for known
// schema keywords).
func childKeyword(parent, keyword string) string {
	return jsonptr.NewBuilder(parent).Token(keyword).String()
}

// evalScope is the dynamic scope of validation: the chain of resource
// roots whose schemas are currently being evaluated against the
// instance. It is used to resolve $dynamicRef per JSON Schema 2020-12
// §8.2.3.2 — when a $dynamicRef is bookended, the validator walks the
// scope outermost-to-innermost looking for a matching $dynamicAnchor.
type evalScope []*Meta

func (s evalScope) push(resource *Meta) evalScope {
	if resource == nil || (len(s) > 0 && s[len(s)-1] == resource) {
		return s
	}
	return append(s, resource)
}

func (s evalScope) findDynamicAnchor(name string) *Meta {
	for _, res := range s {
		if a := res.dynamicAnchors[name]; a != nil {
			return a
		}
	}
	return nil
}

func (m *Meta) Evaluate(name string, in []byte) error {
	return m.evaluate(name, in, nil)
}

func (m *Meta) evaluate(name string, in []byte, scope evalScope) error {
	if !json.Valid(in) {
		return NewErrorWithPosition(name, in, 0, errors.New("invalid JSON"))
	}
	dec := jsontext.NewDecoder(bytes.NewReader(in))
	scope = scope.push(m.resource)

	if o, ok := m.TypeObject(); ok {
		if err := o.validate(name, in, dec, scope); err != nil {
			return err
		}
		if m.resolved != nil {
			target := m.resolved
			if m.dynamic && o.DynamicRef != "" {
				anchorName := strings.TrimPrefix(o.DynamicRef, "#")
				if t := scope.findDynamicAnchor(anchorName); t != nil {
					target = t
				}
			}
			return target.evaluate(childKeyword(name, "$ref"), in, scope)
		}
		return nil
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

func (o *MetaObject) validate(name string, in []byte, dec *jsontext.Decoder, scope evalScope) error {
	off := dec.InputOffset()
	val, err := dec.ReadValue()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return NewErrorWithPosition(name, in, dec.InputOffset(), err)
	}
	return o.validateValue(name, in, off, val, scope)
}

func (o *MetaObject) validateValue(name string, in []byte, off int64, val jsontext.Value, scope evalScope) error {
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
		if err := o.validateObject(name, in, off, val, scope); err != nil {
			return err
		}
	case jsontext.KindBeginArray:
		if err := o.validateArray(name, in, off, val, scope); err != nil {
			return err
		}
	}
	if err := o.validateComposition(name, in, off, val, scope); err != nil {
		return err
	}
	return nil
}

func (o *MetaObject) validateComposition(name string, in []byte, off int64, val jsontext.Value, scope evalScope) error {
	for i := range o.AllOf {
		if err := o.AllOf[i].evaluate(childPath(name, "allOf", i), val, scope); err != nil {
			return err
		}
	}
	if len(o.AnyOf) > 0 {
		matched := false
		for i := range o.AnyOf {
			if o.AnyOf[i].evaluate(childPath(name, "anyOf", i), val, scope) == nil {
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
			if o.OneOf[i].evaluate(childPath(name, "oneOf", i), val, scope) == nil {
				matches++
			}
		}
		if matches != 1 {
			return NewErrorWithPosition(name, in, off,
				fmt.Errorf("oneOf: %d subschemas matched, want exactly 1", matches))
		}
	}
	if o.Not != nil {
		if o.Not.evaluate(childKeyword(name, "not"), val, scope) == nil {
			return NewErrorWithPosition(name, in, off, fmt.Errorf("not: subschema unexpectedly matched"))
		}
	}
	if o.If != nil {
		ifErr := o.If.evaluate(childKeyword(name, "if"), val, scope)
		if ifErr == nil && o.Then != nil {
			if err := o.Then.evaluate(childKeyword(name, "then"), val, scope); err != nil {
				return err
			}
		}
		if ifErr != nil && o.Else != nil {
			if err := o.Else.evaluate(childKeyword(name, "else"), val, scope); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *MetaObject) validateObject(name string, in []byte, off int64, val jsontext.Value, scope evalScope) error {
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		return NewErrorWithPosition(name, in, off, err)
	}
	keys := map[string]struct{}{}
	count := 0
	patternRes, err := compilePatternProperties(o.PatternProperties)
	if err != nil {
		return NewErrorWithPosition(name, in, off, err)
	}
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
		propVal = bytes.Clone(propVal)
		matched := false
		if sub, ok := o.Properties[key]; ok && sub != nil {
			matched = true
			if err := sub.evaluate(childKey(name, key), propVal, scope); err != nil {
				return err
			}
		}
		for _, pp := range patternRes {
			if pp.re.MatchString(key) {
				matched = true
				if pp.schema != nil {
					if err := pp.schema.evaluate(childKey(name, key), propVal, scope); err != nil {
						return err
					}
				}
			}
		}
		if !matched && o.AdditionalProperties != nil {
			matched = true
			if err := o.AdditionalProperties.evaluate(childKey(name, key), propVal, scope); err != nil {
				return err
			}
		}
		if !matched && o.UnevaluatedProperties != nil {
			if err := o.UnevaluatedProperties.evaluate(childKey(name, key), propVal, scope); err != nil {
				return err
			}
		}
		if o.PropertyNames != nil {
			keyBytes, _ := json.Marshal(key)
			if err := o.PropertyNames.evaluate(childKeyword(name, "propertyNames"), keyBytes, scope); err != nil {
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
	if cmp, ok := compareRat(o.MinProperties, big.NewRat(int64(count), 1)); ok && cmp > 0 {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("object has %d properties, minProperties %s", count, o.MinProperties))
	}
	if cmp, ok := compareRat(o.MaxProperties, big.NewRat(int64(count), 1)); ok && cmp < 0 {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("object has %d properties, maxProperties %s", count, o.MaxProperties))
	}
	for prop, deps := range o.DependentRequired {
		if _, present := keys[prop]; !present {
			continue
		}
		for _, d := range deps {
			if _, ok := keys[d]; !ok {
				return NewErrorWithPosition(name, in, off,
					fmt.Errorf("property %q requires %q", prop, d))
			}
		}
	}
	for prop, sub := range o.DependentSchemas {
		if _, present := keys[prop]; !present {
			continue
		}
		if sub == nil {
			continue
		}
		if err := sub.evaluate(jsonptr.NewBuilder(name).Token("dependentSchemas").Token(prop).String(), val, scope); err != nil {
			return err
		}
	}
	return nil
}

type patternRegex struct {
	re     *regexp.Regexp
	schema *Meta
}

func compilePatternProperties(pp map[string]*Meta) ([]patternRegex, error) {
	if len(pp) == 0 {
		return nil, nil
	}
	out := make([]patternRegex, 0, len(pp))
	for pat, sub := range pp {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("patternProperties %q: %w", pat, err)
		}
		out = append(out, patternRegex{re: re, schema: sub})
	}
	return out, nil
}

func (o *MetaObject) validateArray(name string, in []byte, off int64, val jsontext.Value, scope evalScope) error {
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
		items = append(items, bytes.Clone(v))
	}
	if cmp, ok := compareRat(o.MinItems, big.NewRat(int64(len(items)), 1)); ok && cmp > 0 {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("array has %d items, minItems %s", len(items), o.MinItems))
	}
	if cmp, ok := compareRat(o.MaxItems, big.NewRat(int64(len(items)), 1)); ok && cmp < 0 {
		return NewErrorWithPosition(name, in, off,
			fmt.Errorf("array has %d items, maxItems %s", len(items), o.MaxItems))
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
	for i := range o.PrefixItems {
		if i >= len(items) {
			break
		}
		if err := o.PrefixItems[i].evaluate(childPath(name, "prefixItems", i), items[i], scope); err != nil {
			return err
		}
	}
	if o.Items != nil {
		for i := len(o.PrefixItems); i < len(items); i++ {
			if err := o.Items.evaluate(jsonptr.NewBuilder(name).Index(i).String(), items[i], scope); err != nil {
				return err
			}
		}
	} else if o.UnevaluatedItems != nil {
		for i := len(o.PrefixItems); i < len(items); i++ {
			if err := o.UnevaluatedItems.evaluate(childPath(name, "unevaluatedItems", i), items[i], scope); err != nil {
				return err
			}
		}
	}
	if o.Contains != nil {
		matched := 0
		for i, item := range items {
			if o.Contains.evaluate(childPath(name, "contains", i), item, scope) == nil {
				matched++
			}
		}
		minN, hasMin := compareRat(o.MinContains, big.NewRat(int64(matched), 1))
		minRequired := 1
		if hasMin {
			// minContains is treated as the lower bound. matched < min means fail.
			if minN > 0 {
				return NewErrorWithPosition(name, in, off,
					fmt.Errorf("contains matched %d items, minContains %s", matched, o.MinContains))
			}
			minRequired = 0
		}
		if !hasMin && matched < minRequired {
			return NewErrorWithPosition(name, in, off,
				fmt.Errorf("contains matched no items"))
		}
		if cmp, ok := compareRat(o.MaxContains, big.NewRat(int64(matched), 1)); ok && cmp < 0 {
			return NewErrorWithPosition(name, in, off,
				fmt.Errorf("contains matched %d items, maxContains %s", matched, o.MaxContains))
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
	if cmp, ok := compareRat(o.MinLength, big.NewRat(int64(count), 1)); ok && cmp > 0 {
		return fmt.Errorf("string length %d less than minLength %s", count, o.MinLength)
	}
	if cmp, ok := compareRat(o.MaxLength, big.NewRat(int64(count), 1)); ok && cmp < 0 {
		return fmt.Errorf("string length %d greater than maxLength %s", count, o.MaxLength)
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
