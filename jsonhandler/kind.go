package jsonhandler

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/crhntr/jsonschema"
)

// Kind names the control a location renders as. It is written to the
// data-kind attribute of the field's wrapper element.
type Kind string

const (
	KindText       Kind = "text"       // <input> for a string
	KindTextarea   Kind = "textarea"   // <textarea> for a string
	KindNumber     Kind = "number"     // <input type="number"> for a number or integer
	KindCheckbox   Kind = "checkbox"   // <input type="checkbox"> for a boolean
	KindSelect     Kind = "select"     // <select> for an enum
	KindRadio      Kind = "radio"      // radio buttons for an enum
	KindCheckboxes Kind = "checkboxes" // checkboxes for an array of unique enum values
	KindConst      Kind = "const"      // a fixed value; nothing to edit
	KindNull       Kind = "null"       // the null type; nothing to edit
	KindJSON       Kind = "json"       // <textarea> holding any JSON value
	KindObject     Kind = "object"     // a fieldset of properties
	KindArray      Kind = "array"      // an ordered list of items
	KindMap        Kind = "map"        // a list of name/value entries
	KindChoice     Kind = "choice"     // a variant picker and the chosen variant
)

// kind picks the control for sh.
func (sh *shape) kind() Kind {
	switch {
	case sh.ui.Widget == "json":
		return KindJSON
	case len(sh.constant) > 0:
		return KindConst
	case len(sh.enum) > 0:
		if sh.ui.Widget == "radio" {
			return KindRadio
		}
		return KindSelect
	case len(sh.variants) > 0:
		return KindChoice
	}
	types := sh.nonNullTypes()
	if !sh.hasTypes {
		types = sh.inferTypes()
	}
	switch len(types) {
	case 0:
		if sh.hasTypes && sh.nullable() {
			return KindNull
		}
		return KindJSON
	case 1:
	default:
		return KindChoice
	}
	switch types[0] {
	case "string":
		if sh.ui.Widget == "textarea" {
			return KindTextarea
		}
		return KindText
	case "number", "integer":
		return KindNumber
	case "boolean":
		if sh.nullable() {
			return KindChoice
		}
		return KindCheckbox
	case "object":
		switch {
		case sh.nullable():
			return KindChoice
		case len(sh.properties) == 0:
			return KindMap
		}
		return KindObject
	case "array":
		switch {
		case sh.nullable():
			return KindChoice
		case sh.ui.Widget != "list" && sh.uniqueItems && len(sh.prefixItems) == 0 && len(flatten(sh.items...).enum) > 0:
			return KindCheckboxes
		}
		return KindArray
	}
	return KindJSON
}

// inferTypes guesses the type of a schema without a type keyword from the
// keywords that only apply to one type.
func (sh *shape) inferTypes() []jsonschema.SimpleType {
	switch {
	case len(sh.properties) > 0 || len(sh.required) > 0 || len(sh.additional) > 0 || sh.closed || len(sh.minProps) > 0 || len(sh.maxProps) > 0:
		return []jsonschema.SimpleType{"object"}
	case len(sh.items) > 0 || len(sh.prefixItems) > 0 || len(sh.minItems) > 0 || len(sh.maxItems) > 0:
		return []jsonschema.SimpleType{"array"}
	case sh.pattern != "" || sh.format != "" || len(sh.minLength) > 0 || len(sh.maxLength) > 0:
		return []jsonschema.SimpleType{"string"}
	case len(sh.minimum) > 0 || len(sh.maximum) > 0 || len(sh.exclusiveMinimum) > 0 || len(sh.exclusiveMaximum) > 0 || len(sh.multipleOf) > 0:
		return []jsonschema.SimpleType{"number"}
	}
	return nil
}

type choice struct {
	label string
	shape *shape
}

// choices lists the variants of a choice: the oneOf/anyOf members, or one
// per type when the type keyword allows several.
func (sh *shape) choices() []choice {
	if len(sh.variants) > 0 {
		out := make([]choice, len(sh.variants))
		for i, v := range sh.variants {
			out[i] = choice{label: variantLabel(flatten(v), i), shape: sh.withVariant(v)}
		}
		return out
	}
	var out []choice
	for _, t := range sh.types {
		out = append(out, choice{label: string(t), shape: sh.withType(t)})
	}
	return out
}

func variantLabel(own *shape, i int) string {
	switch {
	case own.ui.Label != "":
		return own.ui.Label
	case own.title != "":
		return own.title
	case own.refName != "":
		return own.refName
	case len(own.constant) > 0:
		return displayJSON(own.constant)
	case len(own.types) == 1:
		return string(own.types[0])
	}
	if t := own.inferTypes(); len(t) == 1 {
		return string(t[0])
	}
	return "Option " + strconv.Itoa(i+1)
}

// pick returns the index of the first choice that accepts v, or 0.
func pick(choices []choice, v jsontext.Value) int {
	if len(v) == 0 {
		return 0
	}
	for i, c := range choices {
		if c.shape.accepts(v) {
			return i
		}
	}
	return 0
}

// accepts is a cheap structural check used to pick the variant an existing
// value belongs to. It looks at type, const, enum, required and closed
// properties, and the const/enum of declared properties, which is enough
// to tell apart discriminated unions. It is not validation.
func (sh *shape) accepts(v jsontext.Value) bool {
	if sh.never {
		return false
	}
	if len(sh.constant) > 0 {
		return equalJSON(sh.constant, v)
	}
	if len(sh.enum) > 0 && !slices.ContainsFunc(sh.enum, func(e jsontext.Value) bool { return equalJSON(e, v) }) {
		return false
	}
	t := jsonType(v)
	types := sh.types
	if !sh.hasTypes {
		types = sh.inferTypes()
	}
	if len(types) > 0 && !slices.Contains(types, t) && (t != "integer" || !slices.Contains(types, "number")) {
		return false
	}
	if t != "object" {
		return true
	}
	members := map[string]jsontext.Value{}
	for k, raw := range objectMembers(v) {
		members[k] = raw
	}
	for name := range sh.required {
		if _, ok := members[name]; !ok {
			return false
		}
	}
	for name, raw := range members {
		p, declared := sh.property(name)
		if !declared {
			if sh.closed {
				return false
			}
			continue
		}
		ps := flatten(p.nodes...)
		if len(ps.constant) > 0 || len(ps.enum) > 0 {
			if !ps.accepts(raw) {
				return false
			}
		}
	}
	return true
}

// jsonType returns the JSON Schema type of a value; numbers with no
// fractional part are integers.
func jsonType(v jsontext.Value) jsonschema.SimpleType {
	switch v.Kind() {
	case '"':
		return "string"
	case '0':
		f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
		if err == nil && f == math.Trunc(f) {
			return "integer"
		}
		return "number"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return ""
}

func equalJSON(a, b jsontext.Value) bool {
	eq, err := jsonschema.Equal(a, b)
	return err == nil && eq
}

// objectMembers yields the members of a JSON object in document order.
func objectMembers(v jsontext.Value) func(yield func(string, jsontext.Value) bool) {
	return func(yield func(string, jsontext.Value) bool) {
		dec := jsontext.NewDecoder(strings.NewReader(string(v)))
		if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
			return
		}
		for dec.PeekKind() == '"' {
			tok, err := dec.ReadToken()
			if err != nil {
				return
			}
			name := tok.String()
			raw, err := dec.ReadValue()
			if err != nil {
				return
			}
			if !yield(name, raw.Clone()) {
				return
			}
		}
	}
}

// arrayElements returns the elements of a JSON array.
func arrayElements(v jsontext.Value) []jsontext.Value {
	dec := jsontext.NewDecoder(strings.NewReader(string(v)))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '[' {
		return nil
	}
	var out []jsontext.Value
	for dec.PeekKind() != ']' {
		raw, err := dec.ReadValue()
		if err != nil {
			return out
		}
		out = append(out, raw.Clone())
	}
	return out
}

// stringValue returns the Go string of a JSON string value.
func stringValue(v jsontext.Value) (string, bool) {
	if v.Kind() != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	return s, true
}

func compactJSON(v jsontext.Value) string {
	c := v.Clone()
	if err := c.Compact(); err != nil {
		return string(v)
	}
	return string(c)
}

func indentJSON(v jsontext.Value) string {
	c := v.Clone()
	if err := c.Indent(jsontext.WithIndent("  ")); err != nil {
		return string(v)
	}
	return string(c)
}

// displayJSON shows a JSON value to a person: strings without quotes,
// anything else as compact JSON.
func displayJSON(v jsontext.Value) string {
	if s, ok := stringValue(v); ok {
		return s
	}
	return compactJSON(v)
}

func jsonString(s string) jsontext.Value {
	b, err := json.Marshal(s)
	if err != nil {
		// Marshal only fails on invalid UTF-8 when strict; replace it.
		b, _ = json.Marshal(strings.ToValidUTF8(s, "�"))
	}
	return b
}

// inputType is the type attribute of the <input> for a string or number.
func (sh *shape) inputType(kind Kind) string {
	switch sh.ui.Widget {
	case "password", "color", "tel", "search", "hidden", "range", "email", "url", "date", "time", "datetime-local", "month", "week":
		return sh.ui.Widget
	}
	if kind == KindNumber {
		return "number"
	}
	if sh.writeOnly {
		return "password"
	}
	switch sh.format {
	case "date":
		return "date"
	case "date-time":
		return "datetime-local"
	case "time":
		return "time"
	case "email", "idn-email":
		return "email"
	case "uri", "iri":
		return "url"
	}
	return "text"
}

const (
	localDateTime = "2006-01-02T15:04:05"
	fullTime      = "15:04:05Z07:00"
)

// toControl converts a JSON string to what the HTML control for the format
// expects. Browsers' datetime-local and time inputs have no time zone, so
// date-times and times are shown in UTC.
func toControl(format, s string) string {
	switch format {
	case "date-time":
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC().Format(localDateTime)
		}
	case "time":
		if t, err := time.Parse(fullTime, s); err == nil {
			return t.UTC().Format("15:04:05")
		}
	}
	return s
}

// fromControl converts a control value back to the format's JSON string.
// Values that are not in the control's own syntax are passed through for
// the validator to judge.
func fromControl(format, s string) string {
	switch format {
	case "date-time":
		for _, layout := range []string{"2006-01-02T15:04", localDateTime, "2006-01-02T15:04:05.999999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC().Format(time.RFC3339Nano)
			}
		}
	case "time":
		for _, layout := range []string{"15:04", "15:04:05", "15:04:05.999999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Format("15:04:05.999999999") + "Z"
			}
		}
	}
	return s
}

// numberAttr formats a numeric keyword for an HTML attribute.
func numberAttr(v jsontext.Value) string {
	if _, ok := number(v); !ok {
		return ""
	}
	return strings.TrimSpace(string(v))
}

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// integerKeyword reads a non-negative integer keyword such as maxLength.
func integerKeyword(v jsontext.Value) (int, bool) {
	f, ok := number(v)
	if !ok || f < 0 || f != math.Trunc(f) || f > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

func ordinal(label string, i int) string {
	if label == "" {
		return fmt.Sprintf("Item %d", i+1)
	}
	return fmt.Sprintf("%s %d", label, i+1)
}
