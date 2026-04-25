package jsonschema

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/mail"
	"net/netip"
	"net/url"
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

// evalScope carries dynamic-scope state through validation: the chain
// of resource roots being evaluated (for $dynamicRef per §8.2.3.2) plus
// configuration like whether format keywords assert.
type evalScope struct {
	resources    []*Meta
	assertFormat bool
}

func (s evalScope) push(resource *Meta) evalScope {
	if resource == nil || (len(s.resources) > 0 && s.resources[len(s.resources)-1] == resource) {
		return s
	}
	out := s
	out.resources = append(out.resources, resource)
	return out
}

func (s evalScope) findDynamicAnchor(name string) *Meta {
	for _, res := range s.resources {
		if a := res.dynamicAnchors[name]; a != nil {
			return a
		}
	}
	return nil
}

// annotations record which properties / array indices were "evaluated"
// by a schema's keywords. unevaluatedProperties / unevaluatedItems use
// the union of annotations from siblings (including allOf/anyOf/oneOf
// successes and $ref) to know what's still leftover.
type annotations struct {
	properties map[string]struct{}
	items      map[int]struct{}
}

func (a *annotations) addProperty(key string) {
	if a.properties == nil {
		a.properties = map[string]struct{}{}
	}
	a.properties[key] = struct{}{}
}

func (a *annotations) addItem(idx int) {
	if a.items == nil {
		a.items = map[int]struct{}{}
	}
	a.items[idx] = struct{}{}
}

func (a *annotations) merge(b annotations) {
	for k := range b.properties {
		a.addProperty(k)
	}
	for i := range b.items {
		a.addItem(i)
	}
}

func (m *Meta) Evaluate(name string, in []byte) error {
	_, err := m.evaluate(name, in, evalScope{})
	return err
}

// EvaluateWithFormatAssertion behaves like Evaluate but also enforces
// format keywords as assertions (per the format-assertion vocabulary).
// Spec-default Evaluate treats format as an annotation only.
func (m *Meta) EvaluateWithFormatAssertion(name string, in []byte) error {
	_, err := m.evaluate(name, in, evalScope{assertFormat: true})
	return err
}

func (m *Meta) evaluate(name string, in []byte, scope evalScope) (annotations, error) {
	var ann annotations
	if !json.Valid(in) {
		return ann, NewErrorWithPosition(name, in, 0, errors.New("invalid JSON"))
	}
	dec := jsontext.NewDecoder(bytes.NewReader(in))
	scope = scope.push(m.resource)

	if o, ok := m.TypeObject(); ok {
		off := dec.InputOffset()
		val, err := dec.ReadValue()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ann, nil
			}
			return ann, NewErrorWithPosition(name, in, dec.InputOffset(), err)
		}
		bodyAnn, err := o.validateValue(name, in, off, val, scope)
		if err != nil {
			return ann, err
		}
		ann.merge(bodyAnn)
		if m.resolved != nil {
			target := m.resolved
			if m.dynamic && o.DynamicRef != "" {
				anchorName := strings.TrimPrefix(o.DynamicRef, "#")
				if t := scope.findDynamicAnchor(anchorName); t != nil {
					target = t
				}
			}
			refAnn, err := target.evaluate(childKeyword(name, "$ref"), in, scope)
			if err != nil {
				return ann, err
			}
			ann.merge(refAnn)
		}
		// unevaluatedProperties / unevaluatedItems must see annotations
		// from $ref, so they run after the ref is followed.
		kind := jsontext.NewDecoder(bytes.NewReader(val)).PeekKind()
		if kind == jsontext.KindBeginObject && o.UnevaluatedProperties != nil {
			extraAnn, err := o.validateUnevaluatedProperties(name, in, off, val, scope, ann.properties)
			if err != nil {
				return ann, err
			}
			ann.merge(extraAnn)
		}
		if kind == jsontext.KindBeginArray && o.UnevaluatedItems != nil {
			extraAnn, err := o.validateUnevaluatedItems(name, in, off, val, scope, ann.items)
			if err != nil {
				return ann, err
			}
			ann.merge(extraAnn)
		}
		return ann, nil
	}

	if b, ok := m.TypeBool(); ok {
		return ann, validateMetaTypeBool(name, in, dec, b)
	}

	return ann, nil
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

func (o *MetaObject) validateValue(name string, in []byte, off int64, val jsontext.Value, scope evalScope) (annotations, error) {
	var ann annotations
	kind := jsontext.NewDecoder(bytes.NewReader(val)).PeekKind()
	if o.Type != nil {
		if err := o.validateType(name, in, off, kind, val); err != nil {
			return ann, err
		}
	}
	if o.Enum != nil {
		if err := validateEnum(o.Enum, val); err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
	}
	if len(o.Const) > 0 {
		eq, err := Equal(val, o.Const)
		if err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
		if !eq {
			return ann, NewErrorWithPosition(name, in, off, fmt.Errorf("value does not equal const"))
		}
	}
	switch kind {
	case jsontext.KindNumber:
		if err := o.validateNumber(val); err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
	case jsontext.KindString:
		if err := o.validateString(val, scope); err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
	case jsontext.KindBeginObject:
		objAnn, err := o.validateObjectBody(name, in, off, val, scope)
		if err != nil {
			return ann, err
		}
		ann.merge(objAnn)
	case jsontext.KindBeginArray:
		arrAnn, err := o.validateArrayBody(name, in, off, val, scope)
		if err != nil {
			return ann, err
		}
		ann.merge(arrAnn)
	}
	compAnn, err := o.validateComposition(name, in, off, val, scope)
	if err != nil {
		return ann, err
	}
	ann.merge(compAnn)
	return ann, nil
}

func (o *MetaObject) validateComposition(name string, in []byte, off int64, val jsontext.Value, scope evalScope) (annotations, error) {
	var ann annotations
	for i, sub := range o.AllOf {
		subAnn, err := sub.evaluate(childPath(name, "allOf", i), val, scope)
		if err != nil {
			return ann, err
		}
		ann.merge(subAnn)
	}
	if len(o.AnyOf) > 0 {
		matched := false
		for i, sub := range o.AnyOf {
			subAnn, err := sub.evaluate(childPath(name, "anyOf", i), val, scope)
			if err == nil {
				matched = true
				ann.merge(subAnn)
			}
			_ = i
		}
		if !matched {
			return ann, NewErrorWithPosition(name, in, off, fmt.Errorf("anyOf: no subschema matched"))
		}
	}
	if len(o.OneOf) > 0 {
		matches := 0
		var matchedAnn annotations
		for i, sub := range o.OneOf {
			subAnn, err := sub.evaluate(childPath(name, "oneOf", i), val, scope)
			if err == nil {
				matches++
				matchedAnn = subAnn
			}
		}
		if matches != 1 {
			return ann, NewErrorWithPosition(name, in, off,
				fmt.Errorf("oneOf: %d subschemas matched, want exactly 1", matches))
		}
		ann.merge(matchedAnn)
	}
	if o.Not != nil {
		if _, err := o.Not.evaluate(childKeyword(name, "not"), val, scope); err == nil {
			return ann, NewErrorWithPosition(name, in, off, fmt.Errorf("not: subschema unexpectedly matched"))
		}
	}
	if o.If != nil {
		ifAnn, ifErr := o.If.evaluate(childKeyword(name, "if"), val, scope)
		if ifErr == nil {
			ann.merge(ifAnn)
			if o.Then != nil {
				thenAnn, err := o.Then.evaluate(childKeyword(name, "then"), val, scope)
				if err != nil {
					return ann, err
				}
				ann.merge(thenAnn)
			}
		} else if o.Else != nil {
			elseAnn, err := o.Else.evaluate(childKeyword(name, "else"), val, scope)
			if err != nil {
				return ann, err
			}
			ann.merge(elseAnn)
		}
	}
	return ann, nil
}

// validateObjectBody validates body keywords (properties,
// patternProperties, additionalProperties, required, etc.) and returns
// the set of property names it considered evaluated.
func (o *MetaObject) validateObjectBody(name string, in []byte, off int64, val jsontext.Value, scope evalScope) (annotations, error) {
	var ann annotations
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		return ann, NewErrorWithPosition(name, in, off, err)
	}
	keys := map[string]struct{}{}
	count := 0
	patternRes, err := compilePatternProperties(o.PatternProperties)
	if err != nil {
		return ann, NewErrorWithPosition(name, in, off, err)
	}
	for dec.PeekKind() != jsontext.KindEndObject {
		keyTok, err := dec.ReadToken()
		if err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
		key := keyTok.String()
		keys[key] = struct{}{}
		count++
		propVal, err := dec.ReadValue()
		if err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
		propVal = bytes.Clone(propVal)
		matched := false
		if sub, ok := o.Properties[key]; ok && sub != nil {
			matched = true
			if _, err := sub.evaluate(childKey(name, key), propVal, scope); err != nil {
				return ann, err
			}
		}
		for _, pp := range patternRes {
			if pp.re.MatchString(key) {
				matched = true
				if pp.schema != nil {
					if _, err := pp.schema.evaluate(childKey(name, key), propVal, scope); err != nil {
						return ann, err
					}
				}
			}
		}
		if !matched && o.AdditionalProperties != nil {
			matched = true
			if _, err := o.AdditionalProperties.evaluate(childKey(name, key), propVal, scope); err != nil {
				return ann, err
			}
		}
		if matched {
			ann.addProperty(key)
		}
		if o.PropertyNames != nil {
			keyBytes, _ := json.Marshal(key)
			if _, err := o.PropertyNames.evaluate(childKeyword(name, "propertyNames"), keyBytes, scope); err != nil {
				return ann, err
			}
		}
	}
	for _, req := range o.Required {
		if _, ok := keys[req]; !ok {
			return ann, NewErrorWithPosition(name, in, off,
				fmt.Errorf("missing required property %q", req))
		}
	}
	if cmp, ok := compareRat(o.MinProperties, big.NewRat(int64(count), 1)); ok && cmp > 0 {
		return ann, NewErrorWithPosition(name, in, off,
			fmt.Errorf("object has %d properties, minProperties %s", count, o.MinProperties))
	}
	if cmp, ok := compareRat(o.MaxProperties, big.NewRat(int64(count), 1)); ok && cmp < 0 {
		return ann, NewErrorWithPosition(name, in, off,
			fmt.Errorf("object has %d properties, maxProperties %s", count, o.MaxProperties))
	}
	for prop, deps := range o.DependentRequired {
		if _, present := keys[prop]; !present {
			continue
		}
		for _, d := range deps {
			if _, ok := keys[d]; !ok {
				return ann, NewErrorWithPosition(name, in, off,
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
		depAnn, err := sub.evaluate(jsonptr.NewBuilder(name).Token("dependentSchemas").Token(prop).String(), val, scope)
		if err != nil {
			return ann, err
		}
		ann.merge(depAnn)
	}
	for prop, dep := range o.Dependencies {
		if _, present := keys[prop]; !present {
			continue
		}
		if req, ok := dep.Required(); ok {
			for _, d := range req {
				if _, ok := keys[d]; !ok {
					return ann, NewErrorWithPosition(name, in, off,
						fmt.Errorf("dependency: property %q requires %q", prop, d))
				}
			}
			continue
		}
		if sub := dep.Schema(); sub != nil {
			depAnn, err := sub.evaluate(jsonptr.NewBuilder(name).Token("dependencies").Token(prop).String(), val, scope)
			if err != nil {
				return ann, err
			}
			ann.merge(depAnn)
		}
	}
	return ann, nil
}

// validateUnevaluatedProperties applies o.UnevaluatedProperties to
// every key in val that is NOT in alreadyEvaluated.
func (o *MetaObject) validateUnevaluatedProperties(name string, in []byte, off int64, val jsontext.Value, scope evalScope, alreadyEvaluated map[string]struct{}) (annotations, error) {
	var ann annotations
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		return ann, NewErrorWithPosition(name, in, off, err)
	}
	for dec.PeekKind() != jsontext.KindEndObject {
		keyTok, err := dec.ReadToken()
		if err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
		key := keyTok.String()
		propVal, err := dec.ReadValue()
		if err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
		if _, ok := alreadyEvaluated[key]; ok {
			continue
		}
		if _, err := o.UnevaluatedProperties.evaluate(childKey(name, key), bytes.Clone(propVal), scope); err != nil {
			return ann, err
		}
		ann.addProperty(key)
	}
	return ann, nil
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

// validateArrayBody handles minItems/maxItems/uniqueItems/prefixItems/
// items/contains and returns the set of indices considered evaluated.
func (o *MetaObject) validateArrayBody(name string, in []byte, off int64, val jsontext.Value, scope evalScope) (annotations, error) {
	var ann annotations
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		return ann, NewErrorWithPosition(name, in, off, err)
	}
	var items []jsontext.Value
	for dec.PeekKind() != jsontext.KindEndArray {
		v, err := dec.ReadValue()
		if err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
		items = append(items, bytes.Clone(v))
	}
	if cmp, ok := compareRat(o.MinItems, big.NewRat(int64(len(items)), 1)); ok && cmp > 0 {
		return ann, NewErrorWithPosition(name, in, off,
			fmt.Errorf("array has %d items, minItems %s", len(items), o.MinItems))
	}
	if cmp, ok := compareRat(o.MaxItems, big.NewRat(int64(len(items)), 1)); ok && cmp < 0 {
		return ann, NewErrorWithPosition(name, in, off,
			fmt.Errorf("array has %d items, maxItems %s", len(items), o.MaxItems))
	}
	if o.UniqueItems {
		for i := range items {
			for j := i + 1; j < len(items); j++ {
				eq, err := Equal(items[i], items[j])
				if err != nil {
					return ann, NewErrorWithPosition(name, in, off, err)
				}
				if eq {
					return ann, NewErrorWithPosition(name, in, off,
						fmt.Errorf("array items %d and %d are equal", i, j))
				}
			}
		}
	}
	for i, sub := range o.PrefixItems {
		if i >= len(items) {
			break
		}
		if _, err := sub.evaluate(childPath(name, "prefixItems", i), items[i], scope); err != nil {
			return ann, err
		}
		ann.addItem(i)
	}
	if o.Items != nil {
		for i := len(o.PrefixItems); i < len(items); i++ {
			if _, err := o.Items.evaluate(jsonptr.NewBuilder(name).Index(i).String(), items[i], scope); err != nil {
				return ann, err
			}
			ann.addItem(i)
		}
	}
	if o.Contains != nil {
		matched := 0
		for i, item := range items {
			if _, err := o.Contains.evaluate(childPath(name, "contains", i), item, scope); err == nil {
				matched++
				ann.addItem(i)
			}
		}
		minN, hasMin := compareRat(o.MinContains, big.NewRat(int64(matched), 1))
		minRequired := 1
		if hasMin {
			if minN > 0 {
				return ann, NewErrorWithPosition(name, in, off,
					fmt.Errorf("contains matched %d items, minContains %s", matched, o.MinContains))
			}
			minRequired = 0
		}
		if !hasMin && matched < minRequired {
			return ann, NewErrorWithPosition(name, in, off,
				fmt.Errorf("contains matched no items"))
		}
		if cmp, ok := compareRat(o.MaxContains, big.NewRat(int64(matched), 1)); ok && cmp < 0 {
			return ann, NewErrorWithPosition(name, in, off,
				fmt.Errorf("contains matched %d items, maxContains %s", matched, o.MaxContains))
		}
	}
	return ann, nil
}

// validateUnevaluatedItems applies o.UnevaluatedItems to every array
// index in val that is NOT in alreadyEvaluated.
func (o *MetaObject) validateUnevaluatedItems(name string, in []byte, off int64, val jsontext.Value, scope evalScope, alreadyEvaluated map[int]struct{}) (annotations, error) {
	var ann annotations
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		return ann, NewErrorWithPosition(name, in, off, err)
	}
	i := 0
	for dec.PeekKind() != jsontext.KindEndArray {
		v, err := dec.ReadValue()
		if err != nil {
			return ann, NewErrorWithPosition(name, in, off, err)
		}
		if _, ok := alreadyEvaluated[i]; !ok {
			if _, err := o.UnevaluatedItems.evaluate(childPath(name, "unevaluatedItems", i), bytes.Clone(v), scope); err != nil {
				return ann, err
			}
			ann.addItem(i)
		}
		i++
	}
	return ann, nil
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

func (o *MetaObject) validateString(val jsontext.Value, scope evalScope) error {
	if len(o.MinLength) == 0 && len(o.MaxLength) == 0 && o.Pattern == "" && (o.Format == "" || !scope.assertFormat) {
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
	if o.Format != "" && scope.assertFormat {
		if err := validateFormat(o.Format, s); err != nil {
			return err
		}
	}
	return nil
}

// validateFormat enforces format assertions for known JSON Schema 2020-12
// formats. Unknown formats pass silently (per spec, format is an annotation
// by default; we treat known ones as assertions which matches what the
// suite expects).
func validateFormat(format, s string) error {
	switch format {
	case "ipv4":
		addr, err := netip.ParseAddr(s)
		if err != nil || !addr.Is4() || strings.Contains(s, ":") {
			return fmt.Errorf("not a valid ipv4: %q", s)
		}
	case "ipv6":
		addr, err := netip.ParseAddr(s)
		if err != nil || !addr.Is6() || !strings.Contains(s, ":") || addr.Zone() != "" {
			return fmt.Errorf("not a valid ipv6: %q", s)
		}
	case "date-time":
		if !isDateTime(s) {
			return fmt.Errorf("not a valid date-time: %q", s)
		}
	case "date":
		if !isDate(s) {
			return fmt.Errorf("not a valid date: %q", s)
		}
	case "time":
		if !isTime(s) {
			return fmt.Errorf("not a valid time: %q", s)
		}
	case "duration":
		if !isISO8601Duration(s) {
			return fmt.Errorf("not a valid duration: %q", s)
		}
	case "email", "idn-email":
		// Reject display-name forms ("Name <addr>") by disallowing < >.
		if strings.ContainsAny(s, "<>") {
			return fmt.Errorf("not a valid email: %q", s)
		}
		addr, err := mail.ParseAddress(s)
		if err != nil || addr.Name != "" {
			return fmt.Errorf("not a valid email: %q", s)
		}
		at := strings.LastIndexByte(s, '@')
		if at < 0 {
			return fmt.Errorf("not a valid email: %q", s)
		}
		domain := s[at+1:]
		if strings.HasPrefix(domain, "[") && strings.HasSuffix(domain, "]") {
			ip := domain[1 : len(domain)-1]
			if format == "email" {
				ip = strings.TrimPrefix(ip, "IPv6:")
			}
			if _, err := netip.ParseAddr(ip); err != nil {
				return fmt.Errorf("not a valid email IP literal: %q", s)
			}
		} else if !isHostname(domain) {
			return fmt.Errorf("not a valid email domain: %q", s)
		}
	case "hostname", "idn-hostname":
		if !isHostname(s) {
			return fmt.Errorf("not a valid hostname: %q", s)
		}
	case "uri":
		if !isURI(s, true) {
			return fmt.Errorf("not a valid uri: %q", s)
		}
	case "uri-reference":
		if !isURI(s, false) {
			return fmt.Errorf("not a valid uri-reference: %q", s)
		}
	case "iri":
		if !isIRI(s, true) {
			return fmt.Errorf("not a valid iri: %q", s)
		}
	case "iri-reference":
		if !isIRI(s, false) {
			return fmt.Errorf("not a valid iri-reference: %q", s)
		}
	case "uuid":
		if !isUUID(s) {
			return fmt.Errorf("not a valid uuid: %q", s)
		}
	case "regex":
		if _, err := regexp.Compile(s); err != nil {
			return fmt.Errorf("not a valid regex: %w", err)
		}
	case "json-pointer":
		if err := jsonptr.Pointer(s).Validate(); err != nil {
			return fmt.Errorf("not a valid json-pointer: %w", err)
		}
	case "relative-json-pointer":
		if !isRelativeJSONPointer(s) {
			return fmt.Errorf("not a valid relative-json-pointer: %q", s)
		}
	case "uri-template":
		if !isURITemplate(s) {
			return fmt.Errorf("not a valid uri-template: %q", s)
		}
	}
	return nil
}

var (
	uuidRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hostnameRE = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
	dateRE     = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})$`)
	timeRE     = regexp.MustCompile(`^([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})$`)
	dateTimeRE = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})$`)
)

func isDate(s string) bool {
	m := dateRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	y := atoi(m[1])
	mo := atoi(m[2])
	d := atoi(m[3])
	return validDate(y, mo, d)
}

func isTime(s string) bool {
	m := timeRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	h := atoi(m[1])
	mi := atoi(m[2])
	se := atoi(m[3])
	off := m[5]
	return validTime(h, mi, se, off)
}

func isDateTime(s string) bool {
	m := dateTimeRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	y := atoi(m[1])
	mo := atoi(m[2])
	d := atoi(m[3])
	h := atoi(m[4])
	mi := atoi(m[5])
	se := atoi(m[6])
	off := m[8]
	return validDate(y, mo, d) && validTime(h, mi, se, off)
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func validDate(y, mo, d int) bool {
	if mo < 1 || mo > 12 {
		return false
	}
	if d < 1 || d > daysInMonth(y, mo) {
		return false
	}
	return true
}

func daysInMonth(y, mo int) int {
	switch mo {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if (y%4 == 0 && y%100 != 0) || y%400 == 0 {
			return 29
		}
		return 28
	}
	return 0
}

func validTime(h, mi, se int, offset string) bool {
	if h > 23 || mi > 59 {
		return false
	}
	// Allow leap second (60) only at 23:59 with offset that lands on
	// UTC midnight.
	if se > 60 {
		return false
	}
	if se == 60 {
		// Compute UTC hour/minute by applying offset.
		offH, offM, ok := parseOffset(offset)
		if !ok {
			return false
		}
		// Leap seconds are inserted at end of UTC day. The local
		// time minus offset must equal 23:59:60 UTC.
		hUTC := h - offH
		miUTC := mi - offM
		if miUTC < 0 {
			miUTC += 60
			hUTC--
		}
		if miUTC >= 60 {
			miUTC -= 60
			hUTC++
		}
		if hUTC < 0 {
			hUTC += 24
		}
		if hUTC >= 24 {
			hUTC -= 24
		}
		if hUTC != 23 || miUTC != 59 {
			return false
		}
	}
	if _, _, ok := parseOffset(offset); !ok {
		return false
	}
	return true
}

func parseOffset(s string) (h, m int, ok bool) {
	if s == "Z" || s == "z" {
		return 0, 0, true
	}
	if len(s) != 6 {
		return 0, 0, false
	}
	if s[0] != '+' && s[0] != '-' {
		return 0, 0, false
	}
	h = atoi(s[1:3])
	m = atoi(s[4:6])
	if h > 23 || m > 59 {
		return 0, 0, false
	}
	if s[0] == '-' {
		h, m = -h, -m
	}
	return h, m, true
}

func isUUID(s string) bool { return uuidRE.MatchString(s) }

func isHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if !hostnameRE.MatchString(s) {
		return false
	}
	// RFC 5891 §4.2.3.1: label must not contain "--" at positions 3-4
	// unless it starts with "xn--" (the IDNA A-label prefix).
	for _, label := range strings.Split(s, ".") {
		if len(label) >= 4 && label[2] == '-' && label[3] == '-' {
			lower := strings.ToLower(label)
			if !strings.HasPrefix(lower, "xn--") {
				return false
			}
		}
	}
	return true
}

func isISO8601Duration(s string) bool {
	if !strings.HasPrefix(s, "P") || len(s) < 2 {
		return false
	}
	rest := s[1:]
	// Week form: nW alone.
	if strings.HasSuffix(rest, "W") {
		digits := rest[:len(rest)-1]
		return digits != "" && allASCIIDigits(digits)
	}
	var datePart, timePart string
	if i := strings.Index(rest, "T"); i >= 0 {
		datePart = rest[:i]
		timePart = rest[i+1:]
		if timePart == "" {
			return false
		}
	} else {
		datePart = rest
	}
	if datePart == "" && timePart == "" {
		return false
	}
	if datePart != "" && !isDurationDate(datePart) {
		return false
	}
	if timePart != "" && !isDurationTime(timePart) {
		return false
	}
	return true
}

// isDurationDate validates a duration date-spec per RFC 3339 App A:
// year-only, month-only, day-only, year+month, month+day, or
// year+month+day. year+day without month is invalid.
func isDurationDate(s string) bool {
	return parseDurationFields(s, "YMD", true)
}

// isDurationTime validates a duration time-spec: hour, minute, second,
// hour+minute, minute+second, or hour+minute+second.
func isDurationTime(s string) bool {
	return parseDurationFields(s, "HMS", true)
}

// parseDurationFields walks s consuming `digits letter` segments where
// each letter is one of the markers in order. Skipping the middle
// marker is forbidden when both flanking ones are present
// (RFC 3339 App A constraint).
func parseDurationFields(s, markers string, gapForbidden bool) bool {
	if s == "" {
		return false
	}
	pos := 0
	seen := [3]bool{}
	for pos < len(s) {
		// digits
		start := pos
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
		if pos == start || pos == len(s) {
			return false
		}
		marker := s[pos]
		idx := strings.IndexByte(markers, marker)
		if idx < 0 {
			return false
		}
		// Must appear in increasing order, no duplicates.
		for j := 0; j <= idx; j++ {
			if j == idx {
				if seen[j] {
					return false
				}
				seen[j] = true
			} else if seen[j] {
				continue
			}
		}
		// Reject out-of-order: any later index already seen.
		for j := idx + 1; j < 3; j++ {
			if seen[j] {
				return false
			}
		}
		pos++
	}
	if gapForbidden && seen[0] && seen[2] && !seen[1] {
		return false
	}
	if !seen[0] && !seen[1] && !seen[2] {
		return false
	}
	return true
}

func allASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isRelativeJSONPointer(s string) bool {
	if s == "" {
		return false
	}
	// non-negative-integer ("#" | json-pointer)
	i := 0
	if s[0] == '0' {
		i = 1
	} else {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			return false
		}
	}
	rest := s[i:]
	if rest == "#" {
		return true
	}
	return jsonptr.Pointer(rest).Validate() == nil
}

// isURI uses url.ParseRequestURI / url.Parse for structure and adds an
// ASCII + RFC 3986 character-set check (the stdlib parsers are too
// permissive for RFC 3986 and accept things like spaces, control
// chars, and non-ASCII).
func isURI(s string, requireAbs bool) bool {
	if !isASCII(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isURIChar(s[i]) {
			return false
		}
	}
	if requireAbs {
		u, err := url.Parse(s)
		if err != nil || !u.IsAbs() {
			return false
		}
		return true
	}
	_, err := url.Parse(s)
	return err == nil
}

// isIRI permits non-ASCII per RFC 3987 but still rejects control
// chars and disallowed ASCII (space, <>"`{}|\^).
func isIRI(s string, requireAbs bool) bool {
	for _, c := range s {
		if c < 0x21 || c == ' ' || c == '<' || c == '>' || c == '"' ||
			c == '`' || c == '{' || c == '}' || c == '|' || c == '\\' || c == '^' {
			return false
		}
	}
	if requireAbs {
		u, err := url.Parse(s)
		if err != nil || !u.IsAbs() {
			return false
		}
		return true
	}
	_, err := url.Parse(s)
	return err == nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7E || s[i] < 0x20 {
			return false
		}
	}
	return true
}

// isURIChar reports whether c is in the RFC 3986 unreserved /
// reserved / pct-encoded character set.
func isURIChar(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z',
		c >= 'a' && c <= 'z',
		c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '.', '_', '~',
		':', '/', '?', '#', '[', ']', '@',
		'!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=',
		'%':
		return true
	}
	return false
}

func isURITemplate(s string) bool {
	// Minimal check: balanced { } and no nesting.
	depth := 0
	for _, r := range s {
		switch r {
		case '{':
			if depth > 0 {
				return false
			}
			depth++
		case '}':
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return depth == 0
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
