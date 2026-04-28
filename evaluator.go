package jsonschema

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-json-experiment/json/jsontext"
	"github.com/go-json-experiment/json/v1"

	"github.com/crhntr/jsonschema/jsonptr"
)

// evalCtx threads keyword/instance/source location and dynamic-scope
// state through evaluation. Methods returning evalCtx return a copy so
// each recursive call gets its own location without disturbing the
// caller's.
type evalCtx struct {
	sourceName              string
	in                      []byte
	keywordLocation         string
	absoluteKeywordLocation string
	instanceLocation        string
	scope                   evalScope
}

func (c evalCtx) atKeyword(keyword string) evalCtx {
	c.keywordLocation = jsonptr.NewBuilder(c.keywordLocation).Token(keyword).String()
	if c.absoluteKeywordLocation != "" {
		c.absoluteKeywordLocation = appendFragmentToken(c.absoluteKeywordLocation, keyword)
	}
	return c
}

func (c evalCtx) atKeywordKey(keyword, key string) evalCtx {
	c.keywordLocation = jsonptr.NewBuilder(c.keywordLocation).Token(keyword).Token(key).String()
	if c.absoluteKeywordLocation != "" {
		c.absoluteKeywordLocation = appendFragmentToken(appendFragmentToken(c.absoluteKeywordLocation, keyword), key)
	}
	return c
}

func (c evalCtx) atKeywordIndex(keyword string, i int) evalCtx {
	c.keywordLocation = jsonptr.NewBuilder(c.keywordLocation).Token(keyword).Index(i).String()
	if c.absoluteKeywordLocation != "" {
		c.absoluteKeywordLocation = appendFragmentToken(c.absoluteKeywordLocation, keyword) + "/" + strconv.Itoa(i)
	}
	return c
}

// crossReference resets absoluteKeywordLocation to point at target's
// location in its resource. Caller has already extended keywordLocation
// with the appropriate $ref / $dynamicRef token.
func (c evalCtx) crossReference(target *Schema) evalCtx {
	if target != nil && target.resource != nil {
		c.absoluteKeywordLocation = target.resource.baseURI + "#" + target.pathInResource
	}
	return c
}

func (c evalCtx) baseOutput(valOff int64) Output {
	return Output{
		Valid:                   true,
		KeywordLocation:         c.keywordLocation,
		AbsoluteKeywordLocation: c.absoluteKeywordLocation,
		InstanceLocation:        c.instanceLocation,
		Source:                  byteSource(c.sourceName, c.in, valOff),
	}
}

// appendFragmentToken extends the JSON-pointer fragment of uri with
// token (escaped per RFC 6901). uri is expected to contain a "#";
// callers only invoke this when absoluteKeywordLocation is non-empty
// (i.e. after a $ref has been crossed and the URI is set).
func appendFragmentToken(uri, token string) string {
	base, frag, ok := strings.Cut(uri, "#")
	if !ok {
		return uri + "#" + jsonptr.NewBuilder("").Token(token).String()
	}
	return base + "#" + jsonptr.NewBuilder(frag).Token(token).String()
}

// coveredKeys tracks which object properties and array indices a
// schema's keywords have considered "evaluated", so a sibling
// unevaluatedProperties / unevaluatedItems can skip them. The Story 5
// annotation collection extends this; for now it is a side channel.
type coveredKeys struct {
	properties map[string]struct{}
	items      map[int]struct{}
}

func (c *coveredKeys) addProperty(k string) {
	if c.properties == nil {
		c.properties = map[string]struct{}{}
	}
	c.properties[k] = struct{}{}
}

func (c *coveredKeys) addItem(i int) {
	if c.items == nil {
		c.items = map[int]struct{}{}
	}
	c.items[i] = struct{}{}
}

func (c *coveredKeys) merge(o coveredKeys) {
	for k := range o.properties {
		c.addProperty(k)
	}
	for i := range o.items {
		c.addItem(i)
	}
}

// byteSource computes the 1-based line / 0-based column of offset
// within in. Used to populate Output.Source so failure messages can
// point at the value in the original document.
func byteSource(name string, in []byte, offset int64) Source {
	if offset > int64(len(in)) {
		offset = int64(len(in))
	}
	var line, col int64 = 1, 0
	last := int64(bytes.LastIndexByte(in[:offset], '\n'))
	if last < 0 {
		col = offset
	} else {
		col = offset - last - 1
		line = 1 + int64(bytes.Count(in[:offset], []byte("\n")))
	}
	return Source{Name: name, Line: line, Column: col, Offset: offset}
}

// Validate validates in against m and returns the Output tree. Any
// failure surfaces as a tree of Outputs with Valid:false; pass
// out.AsError() to bridge into Go's error-returning idioms.
func (m *Schema) Validate(name string, in []byte) Output {
	return m.validateRoot(evalCtx{sourceName: name, in: in}, in)
}

// ValidateWithFormatAssertion behaves like Validate but enforces format
// keywords as assertions (per the format-assertion vocabulary). Use
// this when the schema's $vocabulary opts in, or when a caller wants
// strict format checking regardless of vocabulary.
func (m *Schema) ValidateWithFormatAssertion(name string, in []byte) Output {
	ctx := evalCtx{sourceName: name, in: in}
	ctx.scope.assertFormat = true
	return m.validateRoot(ctx, in)
}

func (m *Schema) validateRoot(ctx evalCtx, in []byte) Output {
	if !json.Valid(in) {
		out := ctx.baseOutput(0)
		out.Valid = false
		out.Error = "invalid JSON"
		return out
	}
	dec := jsontext.NewDecoder(bytes.NewReader(in))
	valOff := dec.InputOffset()
	val, err := dec.ReadValue()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return ctx.baseOutput(0)
		}
		out := ctx.baseOutput(dec.InputOffset())
		out.Valid = false
		out.Error = err.Error()
		return out
	}
	out, _ := m.evaluate(ctx, val, valOff)
	return out
}

// evaluate validates val against m. Returns one Output for this
// schema's evaluation (with per-keyword child Errors when invalid)
// plus the set of object properties / array indices that this
// schema's keywords considered evaluated (used by sibling
// unevaluated* keywords).
func (m *Schema) evaluate(ctx evalCtx, val jsontext.Value, valOff int64) (Output, coveredKeys) {
	var covered coveredKeys
	out := ctx.baseOutput(valOff)

	ctx.scope = ctx.scope.push(m.resource)
	if m.resource != nil {
		ctx.scope.skipValidation = m.resource.skipValidation
		if mObj, ok := m.resource.TypeObject(); ok {
			ctx.scope.skipPrefixItems = isPre2020Schema(mObj.Schema)
		}
	}

	if b, ok := m.TypeBool(); ok {
		if !b {
			out.Valid = false
			out.Error = "nothing allowed here"
		}
		return out, covered
	}

	o, ok := m.TypeObject()
	if !ok {
		return out, covered
	}

	var children []Output
	addChild := func(c Output) {
		children = append(children, c)
		if !c.Valid {
			out.Valid = false
		}
	}

	kind := jsontext.NewDecoder(bytes.NewReader(val)).PeekKind()

	if o.Type != nil && !ctx.scope.skipValidation {
		addChild(o.checkType(ctx.atKeyword("type"), val, valOff, kind))
	}
	if o.Enum != nil && !ctx.scope.skipValidation {
		addChild(o.checkEnum(ctx.atKeyword("enum"), val, valOff))
	}
	if len(o.Const) > 0 && !ctx.scope.skipValidation {
		addChild(o.checkConst(ctx.atKeyword("const"), val, valOff))
	}

	if !ctx.scope.skipValidation && kind == jsontext.KindNumber {
		for _, c := range o.checkNumberKeywords(ctx, val, valOff) {
			addChild(c)
		}
	}
	if kind == jsontext.KindString {
		for _, c := range o.checkStringKeywords(ctx, val, valOff) {
			addChild(c)
		}
	}
	if kind == jsontext.KindBeginObject {
		bodyChildren, bodyCovered := o.checkObjectBody(ctx, val, valOff)
		for _, c := range bodyChildren {
			addChild(c)
		}
		covered.merge(bodyCovered)
	}
	if kind == jsontext.KindBeginArray {
		bodyChildren, bodyCovered := o.checkArrayBody(ctx, val, valOff)
		for _, c := range bodyChildren {
			addChild(c)
		}
		covered.merge(bodyCovered)
	}

	compChildren, compCovered := o.checkComposition(ctx, val, valOff)
	for _, c := range compChildren {
		addChild(c)
	}
	covered.merge(compCovered)

	// $ref / $dynamicRef. Per spec the ref result must see the same
	// instance and contributes to coverage.
	if m.resolved != nil {
		target := m.resolved
		refKeyword := "$ref"
		if o.DynamicRef != "" {
			refKeyword = "$dynamicRef"
			if m.dynamic {
				anchorName := dynamicRefAnchor(o.DynamicRef)
				if t := ctx.scope.findDynamicAnchor(anchorName); t != nil {
					target = t
				}
			}
		}
		refCtx := ctx.atKeyword(refKeyword).crossReference(target)
		refOut, refCovered := target.evaluate(refCtx, val, valOff)
		addChild(refOut)
		covered.merge(refCovered)
	}

	if kind == jsontext.KindBeginObject && o.UnevaluatedProperties != nil {
		c, addCovered := o.checkUnevaluatedProperties(ctx, val, valOff, covered.properties)
		addChild(c)
		covered.merge(addCovered)
	}
	if kind == jsontext.KindBeginArray && o.UnevaluatedItems != nil {
		c, addCovered := o.checkUnevaluatedItems(ctx, val, valOff, covered.items)
		addChild(c)
		covered.merge(addCovered)
	}

	for _, c := range children {
		if !c.Valid {
			out.Errors = append(out.Errors, c)
		}
	}
	return out, covered
}

// checkType implements the "type" keyword.
func (o *SchemaObject) checkType(ctx evalCtx, val jsontext.Value, valOff int64, kind jsontext.Kind) Output {
	out := ctx.baseOutput(valOff)
	for _, t := range typeNames(o.Type) {
		if matchesType(t, kind, val) {
			return out
		}
	}
	out.Valid = false
	out.Error = fmt.Sprintf("type %s does not match %s", typeListString(o.Type), kind)
	return out
}

// checkEnum implements the "enum" keyword.
func (o *SchemaObject) checkEnum(ctx evalCtx, val jsontext.Value, valOff int64) Output {
	out := ctx.baseOutput(valOff)
	for _, e := range o.Enum {
		eq, err := Equal(val, e)
		if err != nil {
			out.Valid = false
			out.Error = err.Error()
			return out
		}
		if eq {
			return out
		}
	}
	out.Valid = false
	out.Error = "value is not in enum"
	return out
}

// checkConst implements the "const" keyword.
func (o *SchemaObject) checkConst(ctx evalCtx, val jsontext.Value, valOff int64) Output {
	out := ctx.baseOutput(valOff)
	eq, err := Equal(val, o.Const)
	if err != nil {
		out.Valid = false
		out.Error = err.Error()
		return out
	}
	if !eq {
		out.Valid = false
		out.Error = "value does not equal const"
	}
	return out
}

// checkNumberKeywords implements multipleOf, minimum, maximum,
// exclusiveMinimum, exclusiveMaximum. Each present keyword produces
// one Output.
func (o *SchemaObject) checkNumberKeywords(ctx evalCtx, val jsontext.Value, valOff int64) []Output {
	n, ok := new(big.Rat).SetString(string(val))
	if !ok {
		c := ctx.atKeyword("type").baseOutput(valOff)
		c.Valid = false
		c.Error = fmt.Sprintf("invalid number %s", val)
		return []Output{c}
	}
	var out []Output
	if len(o.MultipleOf) > 0 {
		c := ctx.atKeyword("multipleOf").baseOutput(valOff)
		div, ok := new(big.Rat).SetString(string(o.MultipleOf))
		if !ok || div.Sign() == 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("invalid multipleOf %s", o.MultipleOf)
		} else {
			quot := new(big.Rat).Quo(n, div)
			if !quot.IsInt() {
				c.Valid = false
				c.Error = fmt.Sprintf("not a multiple of %s", o.MultipleOf)
			}
		}
		out = append(out, c)
	}
	if cmp, ok := compareRat(o.Minimum, n); ok {
		c := ctx.atKeyword("minimum").baseOutput(valOff)
		if cmp > 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("less than minimum %s", o.Minimum)
		}
		out = append(out, c)
	}
	if cmp, ok := compareRat(o.Maximum, n); ok {
		c := ctx.atKeyword("maximum").baseOutput(valOff)
		if cmp < 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("greater than maximum %s", o.Maximum)
		}
		out = append(out, c)
	}
	if cmp, ok := compareRat(o.ExclusiveMinimum, n); ok {
		c := ctx.atKeyword("exclusiveMinimum").baseOutput(valOff)
		if cmp >= 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("not strictly greater than exclusiveMinimum %s", o.ExclusiveMinimum)
		}
		out = append(out, c)
	}
	if cmp, ok := compareRat(o.ExclusiveMaximum, n); ok {
		c := ctx.atKeyword("exclusiveMaximum").baseOutput(valOff)
		if cmp <= 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("not strictly less than exclusiveMaximum %s", o.ExclusiveMaximum)
		}
		out = append(out, c)
	}
	return out
}

// checkStringKeywords implements minLength, maxLength, pattern, and
// (when format-assertion is on) format.
func (o *SchemaObject) checkStringKeywords(ctx evalCtx, val jsontext.Value, valOff int64) []Output {
	if len(o.MinLength) == 0 && len(o.MaxLength) == 0 && o.Pattern == "" && (o.Format == "" || !ctx.scope.assertFormat) {
		return nil
	}
	s, err := decodeJSONString(val)
	if err != nil {
		c := ctx.atKeyword("type").baseOutput(valOff)
		c.Valid = false
		c.Error = err.Error()
		return []Output{c}
	}
	count := utf8.RuneCountInString(s)
	var out []Output
	if cmp, ok := compareRat(o.MinLength, big.NewRat(int64(count), 1)); ok {
		c := ctx.atKeyword("minLength").baseOutput(valOff)
		if cmp > 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("string length %d less than minLength %s", count, o.MinLength)
		}
		out = append(out, c)
	}
	if cmp, ok := compareRat(o.MaxLength, big.NewRat(int64(count), 1)); ok {
		c := ctx.atKeyword("maxLength").baseOutput(valOff)
		if cmp < 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("string length %d greater than maxLength %s", count, o.MaxLength)
		}
		out = append(out, c)
	}
	if o.Pattern != "" {
		c := ctx.atKeyword("pattern").baseOutput(valOff)
		re, err := compileECMA262(o.Pattern)
		if err != nil {
			c.Valid = false
			c.Error = fmt.Sprintf("invalid pattern %q: %s", o.Pattern, err)
		} else if !re.MatchString(s) {
			c.Valid = false
			c.Error = fmt.Sprintf("string does not match pattern %q", o.Pattern)
		}
		out = append(out, c)
	}
	if o.Format != "" && ctx.scope.assertFormat {
		c := ctx.atKeyword("format").baseOutput(valOff)
		if err := validateFormat(o.Format, s); err != nil {
			c.Valid = false
			c.Error = err.Error()
		}
		out = append(out, c)
	}
	return out
}

// checkComposition implements allOf / anyOf / oneOf / not /
// if-then-else. Returns one parent Output per keyword present.
func (o *SchemaObject) checkComposition(ctx evalCtx, val jsontext.Value, valOff int64) ([]Output, coveredKeys) {
	var covered coveredKeys
	var out []Output

	if len(o.AllOf) > 0 {
		parent := ctx.atKeyword("allOf").baseOutput(valOff)
		for i, sub := range o.AllOf {
			subOut, subCovered := sub.evaluate(ctx.atKeywordIndex("allOf", i), val, valOff)
			if !subOut.Valid {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
			if subOut.Valid {
				covered.merge(subCovered)
			}
		}
		out = append(out, parent)
	}
	if len(o.AnyOf) > 0 {
		parent := ctx.atKeyword("anyOf").baseOutput(valOff)
		var failed []Output
		matched := false
		for i, sub := range o.AnyOf {
			subOut, subCovered := sub.evaluate(ctx.atKeywordIndex("anyOf", i), val, valOff)
			if subOut.Valid {
				matched = true
				covered.merge(subCovered)
			} else {
				failed = append(failed, subOut)
			}
		}
		if !matched {
			parent.Valid = false
			parent.Error = "no anyOf subschema matched"
			parent.Errors = failed
		}
		out = append(out, parent)
	}
	if len(o.OneOf) > 0 {
		parent := ctx.atKeyword("oneOf").baseOutput(valOff)
		matches := 0
		var matchedCovered coveredKeys
		var perBranch []Output
		for i, sub := range o.OneOf {
			subOut, subCovered := sub.evaluate(ctx.atKeywordIndex("oneOf", i), val, valOff)
			perBranch = append(perBranch, subOut)
			if subOut.Valid {
				matches++
				matchedCovered = subCovered
			}
		}
		if matches != 1 {
			parent.Valid = false
			parent.Error = fmt.Sprintf("oneOf: %d subschemas matched, want exactly 1", matches)
			parent.Errors = perBranch
		} else {
			covered.merge(matchedCovered)
		}
		out = append(out, parent)
	}
	if o.Not != nil {
		parent := ctx.atKeyword("not").baseOutput(valOff)
		subOut, _ := o.Not.evaluate(ctx.atKeyword("not"), val, valOff)
		if subOut.Valid {
			parent.Valid = false
			parent.Error = "not: subschema unexpectedly matched"
		}
		out = append(out, parent)
	}
	if o.If != nil {
		ifOut, ifCovered := o.If.evaluate(ctx.atKeyword("if"), val, valOff)
		// "if" itself never fails the parent; we report nothing for
		// it as a separate Output here. then/else are evaluated based
		// on its outcome, and their Outputs become the parent's.
		if ifOut.Valid {
			covered.merge(ifCovered)
			if o.Then != nil {
				thenOut, thenCovered := o.Then.evaluate(ctx.atKeyword("then"), val, valOff)
				out = append(out, thenOut)
				if thenOut.Valid {
					covered.merge(thenCovered)
				}
			}
		} else if o.Else != nil {
			elseOut, elseCovered := o.Else.evaluate(ctx.atKeyword("else"), val, valOff)
			out = append(out, elseOut)
			if elseOut.Valid {
				covered.merge(elseCovered)
			}
		}
	}
	return out, covered
}

// checkObjectBody implements properties, patternProperties,
// additionalProperties, propertyNames, required, minProperties,
// maxProperties, dependentRequired, dependentSchemas, dependencies.
// Returns one Output per applicable keyword and the set of covered
// property names.
func (o *SchemaObject) checkObjectBody(ctx evalCtx, val jsontext.Value, valOff int64) ([]Output, coveredKeys) {
	var covered coveredKeys
	var out []Output

	patternRes, patErr := compilePatternProperties(o.PatternProperties)
	if patErr != nil {
		c := ctx.atKeyword("patternProperties").baseOutput(valOff)
		c.Valid = false
		c.Error = patErr.Error()
		out = append(out, c)
		return out, covered
	}

	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		c := ctx.atKeyword("type").baseOutput(valOff)
		c.Valid = false
		c.Error = err.Error()
		return []Output{c}, covered
	}

	type propEntry struct {
		key string
		val jsontext.Value
		off int64
	}
	var entries []propEntry
	keys := map[string]struct{}{}
	for dec.PeekKind() != jsontext.KindEndObject {
		keyTok, err := dec.ReadToken()
		if err != nil {
			c := ctx.baseOutput(valOff)
			c.Valid = false
			c.Error = err.Error()
			return []Output{c}, covered
		}
		key := keyTok.String()
		keys[key] = struct{}{}
		propOff := dec.InputOffset()
		propVal, err := dec.ReadValue()
		if err != nil {
			c := ctx.baseOutput(valOff)
			c.Valid = false
			c.Error = err.Error()
			return []Output{c}, covered
		}
		entries = append(entries, propEntry{key: key, val: bytes.Clone(propVal), off: propOff})
	}

	if len(o.Properties) > 0 {
		parent := ctx.atKeyword("properties").baseOutput(valOff)
		for _, e := range entries {
			sub, ok := o.Properties[e.key]
			if !ok || sub == nil {
				continue
			}
			subOut, _ := sub.evaluate(ctx.atKeywordKey("properties", e.key).atInstanceKey(e.key), e.val, e.off)
			covered.addProperty(e.key)
			if !subOut.Valid {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
		}
		out = append(out, parent)
	}
	if len(patternRes) > 0 {
		parent := ctx.atKeyword("patternProperties").baseOutput(valOff)
		for _, e := range entries {
			for _, pp := range patternRes {
				if !pp.re.MatchString(e.key) {
					continue
				}
				if pp.schema == nil {
					covered.addProperty(e.key)
					continue
				}
				ppCtx := ctx.atKeywordKey("patternProperties", pp.pattern).atInstanceKey(e.key)
				subOut, _ := pp.schema.evaluate(ppCtx, e.val, e.off)
				covered.addProperty(e.key)
				if !subOut.Valid {
					parent.Valid = false
					parent.Errors = append(parent.Errors, subOut)
				}
			}
		}
		out = append(out, parent)
	}
	if o.AdditionalProperties != nil {
		parent := ctx.atKeyword("additionalProperties").baseOutput(valOff)
		for _, e := range entries {
			if _, ok := covered.properties[e.key]; ok {
				continue
			}
			subOut, _ := o.AdditionalProperties.evaluate(ctx.atKeyword("additionalProperties").atInstanceKey(e.key), e.val, e.off)
			covered.addProperty(e.key)
			if !subOut.Valid {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
		}
		out = append(out, parent)
	}
	if o.PropertyNames != nil {
		parent := ctx.atKeyword("propertyNames").baseOutput(valOff)
		for _, e := range entries {
			keyBytes, _ := json.Marshal(e.key)
			subOut, _ := o.PropertyNames.evaluate(ctx.atKeyword("propertyNames"), keyBytes, e.off)
			if !subOut.Valid {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
		}
		out = append(out, parent)
	}
	if len(o.Required) > 0 {
		parent := ctx.atKeyword("required").baseOutput(valOff)
		for _, req := range o.Required {
			if _, ok := keys[req]; !ok {
				parent.Valid = false
				if parent.Error == "" {
					parent.Error = fmt.Sprintf("missing required property %q", req)
				} else {
					parent.Error += fmt.Sprintf("; missing required property %q", req)
				}
			}
		}
		out = append(out, parent)
	}
	if len(o.MinProperties) > 0 {
		c := ctx.atKeyword("minProperties").baseOutput(valOff)
		if cmp, ok := compareRat(o.MinProperties, big.NewRat(int64(len(entries)), 1)); ok && cmp > 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("object has %d properties, minProperties %s", len(entries), o.MinProperties)
		}
		out = append(out, c)
	}
	if len(o.MaxProperties) > 0 {
		c := ctx.atKeyword("maxProperties").baseOutput(valOff)
		if cmp, ok := compareRat(o.MaxProperties, big.NewRat(int64(len(entries)), 1)); ok && cmp < 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("object has %d properties, maxProperties %s", len(entries), o.MaxProperties)
		}
		out = append(out, c)
	}
	if len(o.DependentRequired) > 0 {
		parent := ctx.atKeyword("dependentRequired").baseOutput(valOff)
		for prop, deps := range o.DependentRequired {
			if _, present := keys[prop]; !present {
				continue
			}
			for _, d := range deps {
				if _, ok := keys[d]; !ok {
					parent.Valid = false
					if parent.Error != "" {
						parent.Error += "; "
					}
					parent.Error += fmt.Sprintf("property %q requires %q", prop, d)
				}
			}
		}
		out = append(out, parent)
	}
	if len(o.DependentSchemas) > 0 {
		parent := ctx.atKeyword("dependentSchemas").baseOutput(valOff)
		for prop, sub := range o.DependentSchemas {
			if _, present := keys[prop]; !present || sub == nil {
				continue
			}
			subOut, subCovered := sub.evaluate(ctx.atKeywordKey("dependentSchemas", prop), val, valOff)
			if subOut.Valid {
				covered.merge(subCovered)
			} else {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
		}
		out = append(out, parent)
	}
	if len(o.Dependencies) > 0 {
		parent := ctx.atKeyword("dependencies").baseOutput(valOff)
		for prop, dep := range o.Dependencies {
			if dep == nil {
				continue
			}
			if _, present := keys[prop]; !present {
				continue
			}
			if req, ok := dep.Required(); ok {
				for _, d := range req {
					if _, ok := keys[d]; !ok {
						parent.Valid = false
						if parent.Error != "" {
							parent.Error += "; "
						}
						parent.Error += fmt.Sprintf("dependency: property %q requires %q", prop, d)
					}
				}
				continue
			}
			if sub := dep.Schema(); sub != nil {
				subOut, subCovered := sub.evaluate(ctx.atKeywordKey("dependencies", prop), val, valOff)
				if subOut.Valid {
					covered.merge(subCovered)
				} else {
					parent.Valid = false
					parent.Errors = append(parent.Errors, subOut)
				}
			}
		}
		out = append(out, parent)
	}
	return out, covered
}

// atInstanceKey extends instanceLocation with key (RFC 6901 escaped).
func (c evalCtx) atInstanceKey(key string) evalCtx {
	c.instanceLocation = jsonptr.NewBuilder(c.instanceLocation).Token(key).String()
	return c
}

// atInstanceIndex extends instanceLocation with i.
func (c evalCtx) atInstanceIndex(i int) evalCtx {
	c.instanceLocation = jsonptr.NewBuilder(c.instanceLocation).Index(i).String()
	return c
}

// checkUnevaluatedProperties applies o.UnevaluatedProperties to every
// property of val that is not already in covered.
func (o *SchemaObject) checkUnevaluatedProperties(ctx evalCtx, val jsontext.Value, valOff int64, covered map[string]struct{}) (Output, coveredKeys) {
	parent := ctx.atKeyword("unevaluatedProperties").baseOutput(valOff)
	var newCovered coveredKeys
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		parent.Valid = false
		parent.Error = err.Error()
		return parent, newCovered
	}
	for dec.PeekKind() != jsontext.KindEndObject {
		keyTok, err := dec.ReadToken()
		if err != nil {
			parent.Valid = false
			parent.Error = err.Error()
			return parent, newCovered
		}
		key := keyTok.String()
		propOff := dec.InputOffset()
		propVal, err := dec.ReadValue()
		if err != nil {
			parent.Valid = false
			parent.Error = err.Error()
			return parent, newCovered
		}
		if _, ok := covered[key]; ok {
			continue
		}
		subCtx := ctx.atKeyword("unevaluatedProperties").atInstanceKey(key)
		subOut, _ := o.UnevaluatedProperties.evaluate(subCtx, bytes.Clone(propVal), propOff)
		newCovered.addProperty(key)
		if !subOut.Valid {
			parent.Valid = false
			parent.Errors = append(parent.Errors, subOut)
		}
	}
	return parent, newCovered
}

// checkArrayBody implements minItems, maxItems, uniqueItems,
// prefixItems, items, contains. Returns per-keyword Outputs and the
// set of covered indices.
func (o *SchemaObject) checkArrayBody(ctx evalCtx, val jsontext.Value, valOff int64) ([]Output, coveredKeys) {
	var covered coveredKeys
	var out []Output

	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		c := ctx.atKeyword("type").baseOutput(valOff)
		c.Valid = false
		c.Error = err.Error()
		return []Output{c}, covered
	}
	type item struct {
		val jsontext.Value
		off int64
	}
	var items []item
	for dec.PeekKind() != jsontext.KindEndArray {
		off := dec.InputOffset()
		v, err := dec.ReadValue()
		if err != nil {
			c := ctx.baseOutput(valOff)
			c.Valid = false
			c.Error = err.Error()
			return []Output{c}, covered
		}
		items = append(items, item{val: bytes.Clone(v), off: off})
	}

	if len(o.MinItems) > 0 {
		c := ctx.atKeyword("minItems").baseOutput(valOff)
		if cmp, ok := compareRat(o.MinItems, big.NewRat(int64(len(items)), 1)); ok && cmp > 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("array has %d items, minItems %s", len(items), o.MinItems)
		}
		out = append(out, c)
	}
	if len(o.MaxItems) > 0 {
		c := ctx.atKeyword("maxItems").baseOutput(valOff)
		if cmp, ok := compareRat(o.MaxItems, big.NewRat(int64(len(items)), 1)); ok && cmp < 0 {
			c.Valid = false
			c.Error = fmt.Sprintf("array has %d items, maxItems %s", len(items), o.MaxItems)
		}
		out = append(out, c)
	}
	if o.UniqueItems {
		c := ctx.atKeyword("uniqueItems").baseOutput(valOff)
		for i := range items {
			for j := i + 1; j < len(items); j++ {
				eq, err := Equal(items[i].val, items[j].val)
				if err != nil {
					c.Valid = false
					c.Error = err.Error()
					break
				}
				if eq {
					c.Valid = false
					if c.Error != "" {
						c.Error += "; "
					}
					c.Error += fmt.Sprintf("array items %d and %d are equal", i, j)
				}
			}
		}
		out = append(out, c)
	}
	if !ctx.scope.skipPrefixItems && len(o.PrefixItems) > 0 {
		parent := ctx.atKeyword("prefixItems").baseOutput(valOff)
		for i, sub := range o.PrefixItems {
			if i >= len(items) {
				break
			}
			subOut, _ := sub.evaluate(ctx.atKeywordIndex("prefixItems", i).atInstanceIndex(i), items[i].val, items[i].off)
			covered.addItem(i)
			if !subOut.Valid {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
		}
		out = append(out, parent)
	}
	if o.Items != nil {
		parent := ctx.atKeyword("items").baseOutput(valOff)
		startIdx := len(o.PrefixItems)
		if ctx.scope.skipPrefixItems {
			startIdx = 0
		}
		for i := startIdx; i < len(items); i++ {
			subCtx := ctx.atKeyword("items").atInstanceIndex(i)
			subOut, _ := o.Items.evaluate(subCtx, items[i].val, items[i].off)
			covered.addItem(i)
			if !subOut.Valid {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
		}
		out = append(out, parent)
	}
	if o.Contains != nil {
		parent := ctx.atKeyword("contains").baseOutput(valOff)
		matched := 0
		var perBranch []Output
		for i, it := range items {
			subOut, _ := o.Contains.evaluate(ctx.atKeyword("contains").atInstanceIndex(i), it.val, it.off)
			perBranch = append(perBranch, subOut)
			if subOut.Valid {
				matched++
				covered.addItem(i)
			}
		}
		minRequired := 1
		if len(o.MinContains) > 0 {
			if r, ok := new(big.Rat).SetString(string(o.MinContains)); ok {
				if r.Sign() == 0 {
					minRequired = 0
				} else if r.IsInt() {
					if v, ok := new(big.Int).SetString(r.Num().String(), 10); ok {
						if v.IsInt64() {
							minRequired = int(v.Int64())
						}
					}
				}
			}
		}
		if matched < minRequired {
			parent.Valid = false
			parent.Error = fmt.Sprintf("contains matched %d items, minContains %d", matched, minRequired)
			parent.Errors = perBranch
		}
		if cmp, ok := compareRat(o.MaxContains, big.NewRat(int64(matched), 1)); ok && cmp < 0 {
			parent.Valid = false
			if parent.Error != "" {
				parent.Error += "; "
			}
			parent.Error += fmt.Sprintf("contains matched %d items, maxContains %s", matched, o.MaxContains)
		}
		out = append(out, parent)
	}
	return out, covered
}

// checkUnevaluatedItems applies o.UnevaluatedItems to every array
// index in val that is not in covered.
func (o *SchemaObject) checkUnevaluatedItems(ctx evalCtx, val jsontext.Value, valOff int64, covered map[int]struct{}) (Output, coveredKeys) {
	parent := ctx.atKeyword("unevaluatedItems").baseOutput(valOff)
	var newCovered coveredKeys
	dec := jsontext.NewDecoder(bytes.NewReader(val))
	if _, err := dec.ReadToken(); err != nil {
		parent.Valid = false
		parent.Error = err.Error()
		return parent, newCovered
	}
	i := 0
	for dec.PeekKind() != jsontext.KindEndArray {
		off := dec.InputOffset()
		v, err := dec.ReadValue()
		if err != nil {
			parent.Valid = false
			parent.Error = err.Error()
			return parent, newCovered
		}
		if _, already := covered[i]; !already {
			subCtx := ctx.atKeyword("unevaluatedItems").atInstanceIndex(i)
			subOut, _ := o.UnevaluatedItems.evaluate(subCtx, bytes.Clone(v), off)
			newCovered.addItem(i)
			if !subOut.Valid {
				parent.Valid = false
				parent.Errors = append(parent.Errors, subOut)
			}
		}
		i++
	}
	return parent, newCovered
}
