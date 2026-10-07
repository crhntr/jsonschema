package jsonhandler

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/crhntr/jsonschema/jsonptr"
)

// Form controls are named after form paths. A form path is a JSON Pointer
// into the value being edited, except that map entries are addressed by
// position ("~k0", "~k1", …) because their property names are themselves
// form input. "~k" never appears in a pointer built from a property name,
// which escapes "~" as "~0", so entry tokens cannot collide with
// properties.
//
//	$<path>         the value of a scalar control
//	@len<path>      the number of array items or map entries
//	@variant<path>  the chosen oneOf/anyOf/type variant, by index
//	@present<path>  the optional object, array or map at path is included
//	@key<path>      the property name of the map entry at path
//	@action         the edit an edit button asks for; see [applyAction]
const (
	valuePrefix  = "$"
	metaPrefix   = "@"
	metaLen      = "len"
	metaVariant  = "variant"
	metaPresent  = "present"
	metaKey      = "key"
	ActionName   = "@action"
	entryToken   = "~k"
	maxItems     = 1000 // upper bound on @len so a request cannot ask for unbounded work
	maxDepth     = 48   // deeper locations fall back to a JSON text area
	presentValue = "1"
)

func valueName(path string) string      { return valuePrefix + path }
func metaName(kind, path string) string { return metaPrefix + kind + path }

func propertyPath(path, name string) string {
	return string(jsonptr.Pointer(path).Append(name))
}

func itemPath(path string, i int) string { return path + "/" + strconv.Itoa(i) }

func entryPath(path string, i int) string { return path + "/" + entryToken + strconv.Itoa(i) }

// parseName splits a control name into its prefix kind and form path:
// "$/a" → ("$", "/a"), "@len/a" → ("len", "/a"). ok is false for names
// that are not form controls.
func parseName(name string) (kind, path string, ok bool) {
	switch {
	case strings.HasPrefix(name, valuePrefix):
		kind, path = valuePrefix, name[len(valuePrefix):]
	case strings.HasPrefix(name, metaPrefix):
		rest := name[len(metaPrefix):]
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			i = len(rest)
		}
		kind, path = rest[:i], rest[i:]
		if kind == "" || name == ActionName {
			return "", "", false
		}
	default:
		return "", "", false
	}
	return kind, path, path == "" || path[0] == '/'
}

func formName(kind, path string) string {
	if kind == valuePrefix {
		return valueName(path)
	}
	return metaName(kind, path)
}

// within reports whether path is prefix or a location inside it.
func within(path, prefix string) bool {
	return path == prefix || (strings.HasPrefix(path, prefix) && path[len(prefix)] == '/') || prefix == ""
}

// state is the form being edited: the submitted url.Values with
// accessors keyed by form path.
type state url.Values

func (s state) value(path string) (string, bool) {
	v, ok := s[valueName(path)]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[len(v)-1], true
}

func (s state) values(path string) ([]string, bool) {
	v, ok := s[valueName(path)]
	return v, ok
}

func (s state) meta(kind, path string) (string, bool) {
	v, ok := s[metaName(kind, path)]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[len(v)-1], true
}

// count reads @len at path. ok is false when the control is absent; the
// count is clamped to [0, maxItems].
func (s state) count(path string) (int, bool) {
	raw, ok := s.meta(metaLen, path)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, true
	}
	return min(n, maxItems), true
}

func (s state) present(path string) bool {
	_, ok := s.meta(metaPresent, path)
	return ok
}

// touched reports whether any control at or below path was submitted.
func (s state) touched(path string) bool {
	for name := range s {
		if _, p, ok := parseName(name); ok && within(p, path) {
			return true
		}
	}
	return false
}

// rename moves every control at or below from to the same place below to.
func (s state) rename(from, to string) {
	type move struct{ oldName, newName string }
	var moves []move
	for name := range s {
		kind, p, ok := parseName(name)
		if !ok || !within(p, from) || from == "" {
			continue
		}
		moves = append(moves, move{name, formName(kind, to+p[len(from):])})
	}
	moved := make(map[string][]string, len(moves))
	for _, m := range moves {
		moved[m.newName] = s[m.oldName]
		delete(s, m.oldName)
	}
	for name, v := range moved {
		s[name] = v
	}
}

// remove deletes every control at or below path.
func (s state) remove(path string) {
	for name := range s {
		if _, p, ok := parseName(name); ok && within(p, path) {
			delete(s, name)
		}
	}
}

// splitLast returns the parent path and last token of a form path.
func splitLast(path string) (parent, token string) {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return "", ""
	}
	return path[:i], path[i+1:]
}

// position parses an array item token ("3") or entry token ("~k3").
func position(token string) (i int, entry, ok bool) {
	entry = strings.HasPrefix(token, entryToken)
	digits := strings.TrimPrefix(token, entryToken)
	if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return 0, false, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n >= maxItems {
		return 0, false, false
	}
	return n, entry, true
}

func positionPath(parent string, i int, entry bool) string {
	if entry {
		return entryPath(parent, i)
	}
	return itemPath(parent, i)
}

// applyAction performs the edit an edit button names. Actions are an
// operation followed by a form path:
//
//	add<path>     append an array item or map entry
//	remove<path>  remove an array item or map entry, or collapse an optional value
//	up<path>      swap an array item or map entry with the one before it
//	down<path>    swap an array item or map entry with the one after it
//	expand<path>  include a collapsed optional value
//
// Any other action, including the empty one, changes nothing; the form
// is re-rendered with the submitted values, which is how a newly chosen
// variant takes effect.
func applyAction(s state, action string) {
	i := strings.IndexByte(action, '/')
	if i < 0 {
		return
	}
	op, path := action[:i], action[i:]
	switch op {
	case "add":
		n, _ := s.count(path)
		if n < maxItems {
			url.Values(s).Set(metaName(metaLen, path), strconv.Itoa(n+1))
		}
		url.Values(s).Set(metaName(metaPresent, path), presentValue)
	case "expand":
		url.Values(s).Set(metaName(metaPresent, path), presentValue)
	case "remove":
		parent, token := splitLast(path)
		idx, entry, ok := position(token)
		if !ok {
			s.remove(path)
			return
		}
		n, _ := s.count(parent)
		if idx >= n {
			return
		}
		s.remove(path)
		for j := idx + 1; j < n; j++ {
			s.rename(positionPath(parent, j, entry), positionPath(parent, j-1, entry))
		}
		url.Values(s).Set(metaName(metaLen, parent), strconv.Itoa(n-1))
	case "up", "down":
		parent, token := splitLast(path)
		idx, entry, ok := position(token)
		if !ok {
			return
		}
		other := idx - 1
		if op == "down" {
			other = idx + 1
		}
		if n, _ := s.count(parent); other < 0 || other >= n || idx >= n {
			return
		}
		a, b := positionPath(parent, idx, entry), positionPath(parent, other, entry)
		tmp := parent + "/~swap"
		s.rename(a, tmp)
		s.rename(b, a)
		s.rename(tmp, b)
	}
}

// sortedNames returns the control names in s in a stable order, used
// when the form state is written back out as hidden inputs.
func (s state) sortedNames() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		if name != ActionName {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
