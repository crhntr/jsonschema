package jsonhandler

import (
	"cmp"
	"encoding/json/jsontext"
	"iter"
	"slices"
	"strconv"

	"github.com/crhntr/jsonschema/jsonptr"
)

// keepReadOnly makes every readOnly location in submitted hold what it
// holds in original, identified by JSON Pointer: a value the original has
// is put back (along with any parent the submission left out), and one it
// lacks is removed. Locations come from both values, so a submission can
// neither change, add, nor delete a readOnly value. Array items are
// matched by position.
func keepReadOnly(sh *shape, original, submitted jsontext.Value) jsontext.Value {
	locked := map[string]bool{}
	if len(original) > 0 {
		readOnlyPointers(sh, original, "", 0, locked)
	}
	readOnlyPointers(sh, submitted, "", 0, locked)
	var restore, remove [][]string
	for ptr := range locked {
		tokens := slices.Collect(jsonptr.Pointer(ptr).Tokens())
		if len(original) > 0 {
			if _, err := jsonptr.Find(original, jsonptr.Pointer(ptr)); err == nil {
				restore = append(restore, tokens)
				continue
			}
		}
		remove = append(remove, tokens)
	}
	// Restore parents before children and earlier items before later
	// ones; remove children before parents and later items first, so no
	// change moves a location still to be handled.
	slices.SortFunc(restore, comparePointers)
	slices.SortFunc(remove, func(a, b []string) int { return comparePointers(b, a) })
	for _, tokens := range restore {
		v, _ := jsonptr.Find(original, pointerOf(tokens))
		submitted = setAt(submitted, tokens, v)
	}
	for _, tokens := range remove {
		submitted = deleteAt(submitted, tokens)
	}
	if len(submitted) == 0 {
		return jsontext.Value("null")
	}
	return submitted
}

// readOnlyPointers adds the locations in v that sh marks readOnly. Below a
// readOnly location nothing more is needed: the whole value is kept.
func readOnlyPointers(sh *shape, v jsontext.Value, ptr string, depth int, out map[string]bool) {
	switch {
	case sh.readOnly:
		out[ptr] = true
		return
	case depth > maxDepth:
		return
	case sh.kind() == KindChoice:
		if choices := sh.choices(); len(choices) > 0 {
			readOnlyPointers(choices[pick(choices, v)].shape, v, ptr, depth+1, out)
		}
		return
	}
	switch v.Kind() {
	case '{':
		for name, raw := range objectMembers(v) {
			var nodes []node
			if p, ok := sh.property(name); ok {
				nodes = p.nodes
			} else {
				nodes = sh.additional
			}
			readOnlyPointers(flatten(nodes...), raw, propertyPath(ptr, name), depth+1, out)
		}
	case '[':
		for i, item := range arrayElements(v) {
			readOnlyPointers(flatten(sh.itemNodes(i)...), item, itemPath(ptr, i), depth+1, out)
		}
	}
}

// setAt returns v with x at the location the tokens name, creating
// objects for missing parents. An array index past the end is left alone.
func setAt(v jsontext.Value, tokens []string, x jsontext.Value) jsontext.Value {
	if len(tokens) == 0 {
		return x
	}
	token, rest := tokens[0], tokens[1:]
	if v.Kind() == '[' {
		items := arrayElements(v)
		i, err := strconv.Atoi(token)
		switch {
		case err != nil || i < 0 || i > len(items):
			return v
		case i == len(items):
			items = append(items, setAt(nil, rest, x))
		default:
			items[i] = setAt(items[i], rest, x)
		}
		return mustArray(items, v)
	}
	var members []member
	if v.Kind() == '{' {
		for name, raw := range objectMembers(v) {
			members = append(members, member{name: name, value: raw})
		}
	}
	i := slices.IndexFunc(members, func(m member) bool { return m.name == token })
	if i < 0 {
		members = append(members, member{name: token, value: setAt(nil, rest, x)})
	} else {
		members[i].value = setAt(members[i].value, rest, x)
	}
	return mustObject(members, v)
}

// deleteAt returns v without the location the tokens name.
func deleteAt(v jsontext.Value, tokens []string) jsontext.Value {
	if len(tokens) == 0 {
		return nil
	}
	token, rest := tokens[0], tokens[1:]
	switch v.Kind() {
	case '[':
		items := arrayElements(v)
		i, err := strconv.Atoi(token)
		if err != nil || i < 0 || i >= len(items) {
			return v
		}
		if len(rest) == 0 {
			items = slices.Delete(items, i, i+1)
		} else {
			items[i] = deleteAt(items[i], rest)
		}
		return mustArray(items, v)
	case '{':
		var members []member
		for name, raw := range objectMembers(v) {
			switch {
			case name != token:
				members = append(members, member{name: name, value: raw})
			case len(rest) > 0:
				members = append(members, member{name: name, value: deleteAt(raw, rest)})
			}
		}
		return mustObject(members, v)
	}
	return v
}

// comparePointers orders pointers by their tokens, comparing array indices
// as numbers, with a parent before its children.
func comparePointers(a, b []string) int {
	for i := range min(len(a), len(b)) {
		x, errX := strconv.Atoi(a[i])
		y, errY := strconv.Atoi(b[i])
		if errX == nil && errY == nil {
			if c := cmp.Compare(x, y); c != 0 {
				return c
			}
			continue
		}
		if c := cmp.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func pointerTokens(ptr string) iter.Seq[string] { return jsonptr.Pointer(ptr).Tokens() }

func pointerOf(tokens []string) jsonptr.Pointer {
	var p jsonptr.Pointer
	for _, t := range tokens {
		p = p.Append(t)
	}
	return p
}

// mustObject and mustArray encode values that came from valid JSON, which
// cannot fail; if it somehow does, the original value is kept.
func mustObject(members []member, fallback jsontext.Value) jsontext.Value {
	out, err := objectJSON(members)
	if err != nil {
		return fallback
	}
	return out
}

func mustArray(items []jsontext.Value, fallback jsontext.Value) jsontext.Value {
	out, err := arrayJSON(items)
	if err != nil {
		return fallback
	}
	return out
}
