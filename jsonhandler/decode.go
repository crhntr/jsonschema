package jsonhandler

import (
	"bytes"
	"encoding/json/jsontext"
	"math"
	"strconv"
	"strings"
)

// fieldError is a problem with what was entered at a form path. key
// errors belong to a map entry's property-name control.
type fieldError struct {
	path    string
	key     bool
	message string
}

// result is what a field decodes to. value is nil when the field is left
// out of the JSON. provided reports that someone entered something, as
// opposed to a default like an unchecked box; an optional object that
// nothing was provided for is left out. missing reports a required value
// with nothing usable entered. errs are only reported if the enclosing
// value ends up in the JSON.
type result struct {
	value    jsontext.Value
	provided bool
	missing  bool
	errs     []fieldError
}

const (
	messageRequired  = "required"
	messageNumber    = "must be a number"
	messageInteger   = "must be an integer"
	messageJSON      = "must be valid JSON"
	messageChoice    = "must be one of the options"
	messageKey       = "property name required"
	messageDuplicate = "duplicate property name"
)

func (f *Field) decode() result {
	if f.Collapsed {
		return result{}
	}
	switch f.Kind {
	case KindText, KindTextarea:
		if f.submitted() == "" {
			return f.empty(true)
		}
		return result{value: jsonString(fromControl(f.format, f.submitted())), provided: true}
	case KindNumber:
		return f.decodeNumber()
	case KindCheckbox:
		// An unchecked optional box leaves the value out, unless that
		// would mean true by default.
		if !f.Checked && !f.Required && !f.defaultTrue {
			return result{}
		}
		return result{value: jsontext.Value(strconv.FormatBool(f.Checked)), provided: f.Checked}
	case KindSelect, KindRadio:
		if f.submitted() == "" {
			return f.empty(false)
		}
		return f.decodeOption(f.submitted())
	case KindCheckboxes:
		return f.decodeCheckboxes()
	case KindConst:
		if !f.Required {
			return result{}
		}
		return result{value: f.constant}
	case KindNull:
		if !f.Required {
			return result{}
		}
		return result{value: jsontext.Value("null")}
	case KindJSON:
		raw := strings.TrimSpace(f.submitted())
		if raw == "" {
			return f.empty(false)
		}
		v := jsontext.Value(raw)
		if !v.IsValid() {
			return f.fail(messageJSON)
		}
		return result{value: v, provided: true}
	case KindObject, KindMap:
		return f.decodeObject()
	case KindArray:
		return f.decodeArray()
	case KindChoice:
		if f.Choice == nil {
			return result{}
		}
		return f.Choice.decode()
	}
	return result{}
}

// empty is the result of a control left blank: null for a required
// nullable value, the empty string where that is a value, otherwise
// nothing.
func (f *Field) empty(emptyString bool) result {
	switch {
	case !f.Required:
		return result{}
	case f.nullable:
		return result{value: jsontext.Value("null")}
	case emptyString:
		return result{value: jsontext.Value(`""`)}
	}
	return result{missing: true}
}

func (f *Field) fail(message string) result {
	return result{provided: true, errs: []fieldError{{path: f.Path, message: message}}}
}

func (f *Field) decodeNumber() result {
	raw := strings.TrimSpace(f.submitted())
	if raw == "" {
		return f.empty(false)
	}
	v := jsontext.Value(raw)
	if !v.IsValid() || v.Kind() != '0' {
		return f.fail(messageNumber)
	}
	if f.integer {
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || n != math.Trunc(n) {
			return f.fail(messageInteger)
		}
	}
	return result{value: v, provided: true}
}

func (f *Field) decodeOption(raw string) result {
	if !f.jsonOptions {
		return result{value: jsonString(raw), provided: true}
	}
	v := jsontext.Value(raw)
	if !v.IsValid() {
		return f.fail(messageChoice)
	}
	return result{value: v, provided: true}
}

func (f *Field) decodeCheckboxes() result {
	var items []jsontext.Value
	for _, o := range f.Options {
		if !o.Selected {
			continue
		}
		r := f.decodeOption(o.Value)
		if len(r.errs) > 0 {
			return r
		}
		items = append(items, r.value)
	}
	if len(items) == 0 && !f.Required && !f.explicit {
		return result{}
	}
	v, err := arrayJSON(items)
	if err != nil {
		return f.fail(err.Error())
	}
	return result{value: v, provided: len(items) > 0}
}

type member struct {
	name  string
	value jsontext.Value
}

func (f *Field) decodeObject() result {
	var (
		members  []member
		errs     []fieldError
		provided bool
		names    = map[string]bool{}
	)
	for _, cf := range f.Fields {
		name := lastToken(cf.Pointer)
		names[name] = true
		r := cf.decode()
		errs = append(errs, r.errs...)
		provided = provided || r.provided
		switch {
		case r.missing:
			errs = append(errs, fieldError{path: cf.Path, message: messageRequired})
		case r.value != nil:
			members = append(members, member{name: name, value: r.value})
		}
	}
	for _, e := range f.Entries {
		r := e.decode()
		key := strings.ToValidUTF8(e.Key.Value, "�")
		if key == "" && !r.provided {
			continue // a blank entry is ignored
		}
		provided = true
		errs = append(errs, r.errs...)
		switch {
		case key == "":
			errs = append(errs, fieldError{path: e.Path, key: true, message: messageKey})
			continue
		case names[key]:
			errs = append(errs, fieldError{path: e.Path, key: true, message: messageDuplicate})
			continue
		}
		names[key] = true
		switch {
		case r.missing:
			errs = append(errs, fieldError{path: e.Path, message: messageRequired})
		case r.value != nil:
			members = append(members, member{name: key, value: r.value})
		}
	}
	if !f.Required && !provided && !f.explicit {
		return result{}
	}
	v, err := objectJSON(members)
	if err != nil {
		errs = append(errs, fieldError{path: f.Path, message: err.Error()})
	}
	return result{value: v, provided: provided, errs: errs}
}

func (f *Field) decodeArray() result {
	var (
		items    []jsontext.Value
		errs     []fieldError
		provided bool
	)
	for _, cf := range f.Fields {
		r := cf.decode()
		errs = append(errs, r.errs...)
		provided = provided || r.provided
		switch {
		case r.missing:
			errs = append(errs, fieldError{path: cf.Path, message: messageRequired})
		case r.value != nil:
			items = append(items, r.value)
		}
	}
	if !f.Required && !provided && !f.explicit {
		return result{}
	}
	v, err := arrayJSON(items)
	if err != nil {
		errs = append(errs, fieldError{path: f.Path, message: err.Error()})
	}
	return result{value: v, provided: provided, errs: errs}
}

func objectJSON(members []member) (jsontext.Value, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return nil, err
	}
	for _, m := range members {
		if err := enc.WriteToken(jsontext.String(m.name)); err != nil {
			return nil, err
		}
		if err := enc.WriteValue(m.value); err != nil {
			return nil, err
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func arrayJSON(items []jsontext.Value) (jsontext.Value, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return nil, err
	}
	for _, item := range items {
		if err := enc.WriteValue(item); err != nil {
			return nil, err
		}
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// submitted is the control's submitted value.
func (f *Field) submitted() string {
	if f.WriteOnly {
		return f.secret
	}
	return f.Value
}

// lastToken returns the unescaped last reference token of a JSON Pointer.
func lastToken(ptr string) string {
	_, token := splitLast(ptr)
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(token)
}
