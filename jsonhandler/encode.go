package jsonhandler

import (
	"encoding/json/jsontext"
	"net/url"
	"strconv"
	"strings"
)

// encoder writes a JSON value into form state, the inverse of decoding:
// rendering the state shows the value, and submitting it unchanged
// decodes back to an equivalent value.
type encoder struct {
	state     state
	ancestors []*shape
}

func (e *encoder) encode(sh *shape, path string, v jsontext.Value, depth int) {
	kind := sh.kind()
	if depth > maxDepth {
		kind = KindJSON
	}
	set := func(s string) { url.Values(e.state).Set(valueName(path), s) }
	switch kind {
	case KindText, KindTextarea:
		if s, ok := stringValue(v); ok {
			set(toControl(sh.format, s))
		} else if v.Kind() != 'n' {
			set(compactJSON(v))
		}
	case KindNumber:
		if v.Kind() == '0' {
			set(strings.TrimSpace(string(v)))
		}
	case KindCheckbox:
		set(strconv.FormatBool(v.Kind() == 't'))
	case KindSelect, KindRadio:
		if v.Kind() != 'n' || !sh.nullable() {
			set(optionValue(v, hasNonString(sh.enum)))
		}
	case KindCheckboxes:
		jsonOptions := hasNonString(flatten(sh.items...).enum)
		values := []string{""}
		for _, item := range arrayElements(v) {
			values = append(values, optionValue(item, jsonOptions))
		}
		e.state[valueName(path)] = values
		e.markPresent(path)
	case KindJSON:
		set(indentJSON(v))
	case KindObject:
		e.object(sh, path, v, depth)
	case KindMap:
		if v.Kind() == '{' {
			e.markPresent(path)
			e.entries(sh, path, objectMembers(v), depth)
		}
	case KindArray:
		if v.Kind() != '[' {
			return
		}
		e.markPresent(path)
		items := arrayElements(v)
		url.Values(e.state).Set(metaName(metaLen, path), strconv.Itoa(len(items)))
		e.ancestors = append(e.ancestors, sh)
		for i, item := range items {
			e.encode(flatten(sh.itemNodes(i)...), itemPath(path, i), item, depth+1)
		}
		e.ancestors = e.ancestors[:len(e.ancestors)-1]
	case KindChoice:
		choices := sh.choices()
		if len(choices) == 0 {
			return
		}
		i := pick(choices, v)
		url.Values(e.state).Set(metaName(metaVariant, path), strconv.Itoa(i))
		e.encode(choices[i].shape, path, v, depth+1)
	}
}

func (e *encoder) object(sh *shape, path string, v jsontext.Value, depth int) {
	if v.Kind() != '{' {
		return
	}
	e.markPresent(path)
	e.ancestors = append(e.ancestors, sh)
	defer func() { e.ancestors = e.ancestors[:len(e.ancestors)-1] }()
	var extra []member
	for name, raw := range objectMembers(v) {
		p, ok := sh.property(name)
		if !ok {
			extra = append(extra, member{name: name, value: raw})
			continue
		}
		child := flatten(p.nodes...)
		if child.never {
			continue
		}
		e.encode(child, propertyPath(path, name), raw, depth+1)
	}
	if len(extra) > 0 {
		e.entries(sh, path, func(yield func(string, jsontext.Value) bool) {
			for _, m := range extra {
				if !yield(m.name, m.value) {
					return
				}
			}
		}, depth)
	}
}

func (e *encoder) entries(sh *shape, path string, members func(func(string, jsontext.Value) bool), depth int) {
	values := sh.additional
	if len(values) == 0 {
		values = []node{{}}
	}
	vs := flatten(values...)
	e.ancestors = append(e.ancestors, sh)
	defer func() { e.ancestors = e.ancestors[:len(e.ancestors)-1] }()
	n := 0
	for name, raw := range members {
		if n == maxItems {
			break
		}
		p := entryPath(path, n)
		url.Values(e.state).Set(metaName(metaKey, p), name)
		e.encode(vs, p, raw, depth+1)
		n++
	}
	url.Values(e.state).Set(metaName(metaLen, path), strconv.Itoa(n))
}

func (e *encoder) markPresent(path string) {
	url.Values(e.state).Set(metaName(metaPresent, path), presentValue)
}

func hasNonString(values []jsontext.Value) bool {
	for _, v := range values {
		if v.Kind() != '"' {
			return true
		}
	}
	return false
}
