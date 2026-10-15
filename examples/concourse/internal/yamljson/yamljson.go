// Package yamljson converts between YAML node trees and JSON so a JSON
// Schema can check YAML, and so values edited as JSON can be written back
// into the original YAML without disturbing the rest of the file.
package yamljson

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/crhntr/jsonschema/jsonptr"
)

// Pos is a position in YAML source, counted from 1.
type Pos struct {
	Line, Column int
}

// Positions maps the JSON Pointer of every value to where it is written in
// the YAML. For an object member that is the member's key.
type Positions map[string]Pos

// At returns the position of ptr, or of the nearest enclosing value that
// has one.
func (p Positions) At(ptr string) Pos {
	for {
		if pos, ok := p[ptr]; ok {
			return pos
		}
		i := strings.LastIndexByte(ptr, '/')
		if i < 0 {
			return p[""]
		}
		ptr = ptr[:i]
	}
}

// Parse reads one YAML document.
func Parse(src []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 {
		// An empty file is an empty mapping.
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	return &doc, nil
}

// Decode converts a YAML node to JSON. Mappings keep their key order,
// aliases are expanded, and merge keys ("<<") are applied with explicit
// keys taking precedence.
func Decode(n *yaml.Node) (jsontext.Value, Positions, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	pos := Positions{}
	if err := decode(enc, n, "", pos, 0); err != nil {
		return nil, nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), pos, nil
}

// maxDepth bounds alias expansion, which could otherwise recurse forever.
const maxDepth = 256

func decode(enc *jsontext.Encoder, n *yaml.Node, ptr string, pos Positions, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%s: nested too deeply", ptr)
	}
	if _, ok := pos[ptr]; !ok {
		pos[ptr] = Pos{n.Line, n.Column}
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return enc.WriteToken(jsontext.Null)
		}
		return decode(enc, n.Content[0], ptr, pos, depth+1)
	case yaml.AliasNode:
		return decode(enc, n.Alias, ptr, pos, depth+1)
	case yaml.SequenceNode:
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for i, item := range n.Content {
			if err := decode(enc, item, ptr+"/"+strconv.Itoa(i), pos, depth+1); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndArray)
	case yaml.MappingNode:
		pairs, err := members(n, depth)
		if err != nil {
			return fmt.Errorf("%s: %w", ptr, err)
		}
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		for _, p := range pairs {
			child := string(jsonptr.Pointer(ptr).Append(p.key.Value))
			pos[child] = Pos{p.key.Line, p.key.Column}
			if err := enc.WriteToken(jsontext.String(p.key.Value)); err != nil {
				return err
			}
			if err := decode(enc, p.value, child, pos, depth+1); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndObject)
	case yaml.ScalarNode:
		return scalar(enc, n)
	}
	return fmt.Errorf("%s: unsupported YAML node kind %d", ptr, n.Kind)
}

type pair struct{ key, value *yaml.Node }

// members lists a mapping's keys in order, with merged keys first and
// explicit keys overriding them.
func members(n *yaml.Node, depth int) ([]pair, error) {
	var merged, explicit []pair
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("line %d: only string keys can become JSON", k.Line)
		}
		if k.ShortTag() == "!!merge" {
			sources := []*yaml.Node{v}
			if resolve(v).Kind == yaml.SequenceNode {
				sources = resolve(v).Content
			}
			for _, s := range sources {
				s = resolve(s)
				if s.Kind != yaml.MappingNode {
					return nil, fmt.Errorf("line %d: a merge key needs a mapping", v.Line)
				}
				if depth > maxDepth {
					return nil, errors.New("merges nested too deeply")
				}
				inner, err := members(s, depth+1)
				if err != nil {
					return nil, err
				}
				merged = setPairs(merged, inner...)
			}
			continue
		}
		explicit = setPairs(explicit, pair{k, v})
	}
	return setPairs(merged, explicit...), nil
}

// setPairs adds pairs to list, replacing any with the same key in place.
func setPairs(list []pair, add ...pair) []pair {
next:
	for _, a := range add {
		for i, p := range list {
			if p.key.Value == a.key.Value {
				list[i] = a
				continue next
			}
		}
		list = append(list, a)
	}
	return list
}

func resolve(n *yaml.Node) *yaml.Node {
	for i := 0; n.Kind == yaml.AliasNode && i < maxDepth; i++ {
		n = n.Alias
	}
	return n
}

func scalar(enc *jsontext.Encoder, n *yaml.Node) error {
	switch n.ShortTag() {
	case "!!null":
		return enc.WriteToken(jsontext.Null)
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.Bool(b))
	case "!!int":
		var i int64
		if err := n.Decode(&i); err != nil {
			var u uint64
			if err := n.Decode(&u); err != nil {
				return fmt.Errorf("line %d: %w", n.Line, err)
			}
			return enc.WriteToken(jsontext.Uint(u))
		}
		return enc.WriteToken(jsontext.Int(i))
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return err
		}
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("line %d: %s has no JSON form", n.Line, n.Value)
		}
		return enc.WriteToken(jsontext.Float(f))
	}
	// Strings, and the types JSON lacks (timestamps, binary), as written.
	return enc.WriteToken(jsontext.String(n.Value))
}

// Encode converts JSON to a YAML node, keeping object member order.
// Strings with line breaks are written as literal blocks, which is how
// scripts in pipeline files are usually written.
func Encode(v jsontext.Value) (*yaml.Node, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	n, err := encode(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one JSON value")
	}
	return n, nil
}

func encode(dec *jsontext.Decoder) (*yaml.Node, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	switch tok.Kind() {
	case '{':
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for dec.PeekKind() != '}' {
			k, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			// The token is only valid until the next read.
			key := str(k.String())
			v, err := encode(dec)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, key, v)
		}
		_, err := dec.ReadToken()
		return n, err
	case '[':
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for dec.PeekKind() != ']' {
			v, err := encode(dec)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, v)
		}
		_, err := dec.ReadToken()
		return n, err
	case '"':
		s := str(tok.String())
		if strings.Contains(s.Value, "\n") {
			s.Style = yaml.LiteralStyle
		}
		return s, nil
	case '0':
		raw := tok.String()
		tag := "!!int"
		if strings.ContainsAny(raw, ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: raw}, nil
	case 't', 'f':
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: tok.String()}, nil
	case 'n':
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	return nil, fmt.Errorf("unexpected JSON token %v", tok)
}

func str(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// ErrShared reports a location defined through an anchor or a merge key,
// which cannot be replaced without changing every place that uses it.
var ErrShared = errors.New("defined through a YAML anchor or merge key")

// Find returns the node written at ptr. It does not follow aliases or
// merge keys, since a node reached through one is shared.
func Find(doc *yaml.Node, ptr jsonptr.Pointer) (*yaml.Node, error) {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for tok := range ptr.Tokens() {
		next, err := child(n, tok)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ptr, err)
		}
		n = next
	}
	if n.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("%s: %w", ptr, ErrShared)
	}
	return n, nil
}

func child(n *yaml.Node, tok string) (*yaml.Node, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return nil, ErrShared
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i]; k.Value == tok && k.ShortTag() != "!!merge" {
				return n.Content[i+1], nil
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].ShortTag() == "!!merge" {
				return nil, ErrShared
			}
		}
		return nil, fmt.Errorf("no key %q", tok)
	case yaml.SequenceNode:
		i, err := strconv.Atoi(tok)
		if err != nil || i < 0 || i >= len(n.Content) {
			return nil, fmt.Errorf("no item %q", tok)
		}
		return n.Content[i], nil
	}
	return nil, fmt.Errorf("cannot look up %q in a scalar", tok)
}

// Replace puts the JSON value v at ptr. It changes only what differs:
// unchanged values keep their nodes (with their comments, quoting and
// aliases), changed mappings keep their key order with new keys added at
// the end, and everything outside ptr is untouched. A changed node that
// carries an anchor is refused with ErrShared, since the change would
// reach every alias of it.
func Replace(doc *yaml.Node, ptr jsonptr.Pointer, v jsontext.Value) error {
	old, err := Find(doc, ptr)
	if err != nil {
		return err
	}
	n, err := Merge(old, v)
	if err != nil {
		return fmt.Errorf("%s%w", ptr, err)
	}
	*old = *n
	return nil
}

// Merge returns a node for v that reuses what stays the same in old, as
// Replace describes. old is not modified.
func Merge(old *yaml.Node, v jsontext.Value) (*yaml.Node, error) { return merge(old, v) }

// merge returns a node for v that reuses as much of old as stays the same.
func merge(old *yaml.Node, v jsontext.Value) (*yaml.Node, error) {
	if same, err := equal(old, v); err != nil || same {
		return old, err
	}
	if old.Anchor != "" {
		return nil, fmt.Errorf(": %w (&%s)", ErrShared, old.Anchor)
	}
	var n *yaml.Node
	var err error
	switch {
	case old.Kind == yaml.MappingNode && v.Kind() == '{' && !hasMerge(old):
		n, err = mergeMapping(old, v)
	case old.Kind == yaml.SequenceNode && v.Kind() == '[':
		n, err = mergeSequence(old, v)
	default:
		// A scalar, an alias, or a change of kind: the new value replaces it.
		n, err = Encode(v)
	}
	if err != nil {
		return nil, err
	}
	n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
	return n, nil
}

func mergeMapping(old *yaml.Node, v jsontext.Value) (*yaml.Node, error) {
	var members []member
	if err := eachMember(v, func(m member) { members = append(members, m) }); err != nil {
		return nil, err
	}
	n := *old
	n.Content = nil
	used := map[string]bool{}
	for i := 0; i+1 < len(old.Content); i += 2 {
		k := old.Content[i]
		for _, m := range members {
			if m.key == k.Value {
				child, err := merge(old.Content[i+1], m.value)
				if err != nil {
					return nil, fmt.Errorf("/%s%w", k.Value, err)
				}
				n.Content = append(n.Content, k, child)
				used[m.key] = true
			}
		}
	}
	for _, m := range members {
		if !used[m.key] {
			child, err := Encode(m.value)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, str(m.key), child)
		}
	}
	return &n, nil
}

func mergeSequence(old *yaml.Node, v jsontext.Value) (*yaml.Node, error) {
	var items []jsontext.Value
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	if _, err := dec.ReadToken(); err != nil {
		return nil, err
	}
	for dec.PeekKind() != ']' {
		item, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		items = append(items, item.Clone())
	}
	n := *old
	n.Content = nil
	for i, item := range items {
		var child *yaml.Node
		var err error
		if i < len(old.Content) {
			child, err = merge(old.Content[i], item)
		} else {
			child, err = Encode(item)
		}
		if err != nil {
			return nil, fmt.Errorf("/%d%w", i, err)
		}
		n.Content = append(n.Content, child)
	}
	return &n, nil
}

type member struct {
	key   string
	value jsontext.Value
}

func eachMember(v jsontext.Value, fn func(member)) error {
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	for dec.PeekKind() != '}' {
		k, err := dec.ReadToken()
		if err != nil {
			return err
		}
		key := k.String()
		value, err := dec.ReadValue()
		if err != nil {
			return err
		}
		fn(member{key: key, value: value.Clone()})
	}
	return nil
}

func hasMerge(n *yaml.Node) bool {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].ShortTag() == "!!merge" {
			return true
		}
	}
	return false
}

// equal reports whether a node stands for the same JSON as v.
func equal(n *yaml.Node, v jsontext.Value) (bool, error) {
	a, _, err := Decode(n)
	if err != nil {
		return false, err
	}
	a, b := a.Clone(), v.Clone()
	if err := a.Canonicalize(); err != nil {
		return false, err
	}
	if err := b.Canonicalize(); err != nil {
		return false, err
	}
	return bytes.Equal(a, b), nil
}

// Append adds the JSON value v to the end of the sequence at ptr.
func Append(doc *yaml.Node, ptr jsonptr.Pointer, v jsontext.Value) error {
	seq, err := Find(doc, ptr)
	if err != nil {
		return err
	}
	if seq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s is not a list", ptr)
	}
	n, err := Encode(v)
	if err != nil {
		return err
	}
	seq.Content = append(seq.Content, n)
	return nil
}

// Remove deletes the item at ptr from its sequence.
func Remove(doc *yaml.Node, ptr jsonptr.Pointer) error {
	parentPtr, last := split(ptr)
	seq, err := Find(doc, parentPtr)
	if err != nil {
		return err
	}
	i, err := strconv.Atoi(last)
	if seq.Kind != yaml.SequenceNode || err != nil || i < 0 || i >= len(seq.Content) {
		return fmt.Errorf("%s is not a list item", ptr)
	}
	seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
	return nil
}

// plainMergeKeys clears the tag yaml.v3 puts on decoded merge keys, which
// it would otherwise write out as "!!merge <<:". An untagged "<<" still
// resolves to a merge key.
func plainMergeKeys(n *yaml.Node, depth int) {
	if depth > maxDepth {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			if k := n.Content[i]; k.Tag == "!!merge" {
				k.Tag = ""
			}
		}
	}
	for _, c := range n.Content {
		plainMergeKeys(c, depth+1)
	}
}

func split(ptr jsonptr.Pointer) (jsonptr.Pointer, string) {
	s := string(ptr)
	i := strings.LastIndexByte(s, '/')
	if i < 0 {
		return "", ""
	}
	return jsonptr.Pointer(s[:i]), s[i+1:]
}

// Format writes the document as YAML with two-space indentation.
func Format(doc *yaml.Node) ([]byte, error) { return format(doc, false) }

// Item writes one sequence item as the text that would stand for it in a
// file: "- " at indent spaces, with the item's own head comment left out
// (it stays above the item in the file). With compact, nested sequences
// are written flush with their key, as in "plan:\n- get: x".
func Item(item *yaml.Node, indent int, compact bool) ([]byte, error) {
	c := *item
	c.HeadComment = ""
	text, err := format(&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{&c}}, compact)
	if err != nil {
		return nil, err
	}
	pad := strings.Repeat(" ", indent)
	var out bytes.Buffer
	for line := range strings.Lines(string(text)) {
		if strings.TrimSpace(line) != "" {
			out.WriteString(pad)
		}
		out.WriteString(line)
	}
	return out.Bytes(), nil
}

func format(doc *yaml.Node, compact bool) ([]byte, error) {
	plainMergeKeys(doc, 0)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if compact {
		enc.CompactSeqIndent()
	}
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
