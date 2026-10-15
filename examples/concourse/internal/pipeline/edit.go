package pipeline

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/crhntr/jsonschema/jsonptr"

	"github.com/crhntr/jsonschema/examples/concourse/internal/yamljson"
)

// Edits change text, not trees. An edit to a job, resource or resource
// type re-renders that one list item and puts it in place of the item's
// lines, so the rest of the file (its comments, blank lines, quoting and
// indentation) stays byte for byte as it was, and a diff of the file shows
// only what changed.

// Replace puts v in place of the list item at ptr (such as "/jobs/2") and
// returns the updated document; d is unchanged. Parts of the item that v
// does not change keep their comments, quoting and aliases.
func (d *Document) Replace(ptr string, v jsontext.Value) (*Document, error) {
	item, seq, err := d.item(ptr)
	if err != nil {
		return nil, err
	}
	merged, err := yamljson.Merge(item, v)
	if err != nil {
		return nil, fmt.Errorf("%s%w", ptr, err)
	}
	if merged == item {
		return d, nil // nothing changed
	}
	text, err := yamljson.Item(merged, item.Column-3, d.compact(seq))
	if err != nil {
		return nil, err
	}
	start, end := d.span(item.Line, item.Column-2)
	return d.splice(start, end, text)
}

// Append adds v to the end of the list of k and returns the updated
// document, creating the list when the file has none.
func (d *Document) Append(k Kind, v jsontext.Value) (*Document, error) {
	n, err := yamljson.Encode(v)
	if err != nil {
		return nil, err
	}
	seq, err := yamljson.Find(d.node, jsonptr.Pointer("/"+string(k)))
	if err != nil || seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 || len(seq.Content) == 0 {
		return d.appendList(k, n, seq)
	}
	last := seq.Content[len(seq.Content)-1]
	text, err := yamljson.Item(n, last.Column-3, d.compact(seq))
	if err != nil {
		return nil, err
	}
	_, end := d.span(last.Line, last.Column-2)
	return d.splice(end+1, end, text)
}

// appendList handles a list that is missing, empty or written in flow
// style: the list is written again in block style, or added at the end.
func (d *Document) appendList(k Kind, item, seq *yaml.Node) (*Document, error) {
	if seq != nil && seq.Kind == yaml.SequenceNode {
		key := d.keyOf(seq)
		items := append(append([]*yaml.Node(nil), seq.Content...), item)
		block := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
		var text bytes.Buffer
		text.WriteString(strings.Repeat(" ", key.Column-1) + key.Value + ":\n")
		for _, it := range block.Content {
			t, err := yamljson.Item(it, key.Column-1, true)
			if err != nil {
				return nil, err
			}
			text.Write(t)
		}
		start, end := d.span(key.Line, key.Column-1)
		return d.splice(start, max(end, seq.Line), text.Bytes())
	}
	t, err := yamljson.Item(item, 0, true)
	if err != nil {
		return nil, err
	}
	src := bytes.TrimRight(d.src, "\n")
	var text bytes.Buffer
	text.Write(src)
	if len(src) > 0 {
		text.WriteString("\n\n")
	}
	text.WriteString(string(k) + ":\n")
	text.Write(t)
	return Parse(text.Bytes())
}

// Remove deletes the list item at ptr, with the comment lines just above
// it, and returns the updated document.
func (d *Document) Remove(ptr string) (*Document, error) {
	item, _, err := d.item(ptr)
	if err != nil {
		return nil, err
	}
	start, end := d.span(item.Line, item.Column-2)
	lines := d.lines()
	for start > 1 && strings.HasPrefix(strings.TrimSpace(lines[start-2]), "#") {
		start--
	}
	// Take the blank lines that separated it from the next item, but not
	// the ones that separate the list from what follows it.
	next := end
	for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
		next++
	}
	dash := strings.Repeat(" ", item.Column-3) + "- "
	if next > end && next < len(lines) && strings.HasPrefix(lines[next], dash) {
		end = next
	}
	return d.splice(start, end, nil)
}

// item finds a list item by pointer, refusing items reached through an
// alias or merge key.
func (d *Document) item(ptr string) (item, seq *yaml.Node, err error) {
	i := strings.LastIndexByte(ptr, '/')
	parent, last := ptr[:max(i, 0)], ptr[i+1:]
	if _, err := strconv.Atoi(last); err != nil {
		return nil, nil, fmt.Errorf("%s is not a list item", ptr)
	}
	if seq, err = yamljson.Find(d.node, jsonptr.Pointer(parent)); err != nil {
		return nil, nil, err
	}
	if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
		return nil, nil, fmt.Errorf("%s is not in a block list", ptr)
	}
	item, err = yamljson.Find(d.node, jsonptr.Pointer(ptr))
	return item, seq, err
}

// span returns the lines (from 1, inclusive) of something that starts on
// line and continues on the following lines indented more than deeperThan
// spaces. A list item's dash is at Column-3 spaces and its content at
// Column-1, so an item spans with deeperThan Column-2: the next item, or a
// comment above it, stops the span.
func (d *Document) span(line, deeperThan int) (start, end int) {
	lines := d.lines()
	start, end = line, line
	for i := line; i < len(lines); i++ { // lines[i] is line i+1
		text := lines[i]
		if strings.TrimSpace(text) == "" {
			continue
		}
		if indent(text) <= deeperThan {
			break
		}
		end = i + 1
	}
	return start, end
}

// splice replaces lines start..end (from 1, inclusive) with text. A start
// past end inserts text before line start.
func (d *Document) splice(start, end int, text []byte) (*Document, error) {
	lines := d.lines()
	var out bytes.Buffer
	for _, l := range lines[:start-1] {
		out.WriteString(l + "\n")
	}
	out.Write(text)
	for _, l := range lines[min(end, len(lines)):] {
		out.WriteString(l + "\n")
	}
	return Parse(out.Bytes())
}

func (d *Document) lines() []string {
	return strings.Split(strings.TrimSuffix(string(d.src), "\n"), "\n")
}

// lineOf returns the text of the line a node starts on.
func (d *Document) lineOf(n *yaml.Node) string {
	lines := d.lines()
	if n.Line < 1 || n.Line > len(lines) {
		return ""
	}
	return lines[n.Line-1]
}

// keyOf returns the key node of a top-level value.
func (d *Document) keyOf(v *yaml.Node) *yaml.Node {
	root := d.node.Content[0]
	for i := 1; i < len(root.Content); i += 2 {
		if root.Content[i] == v {
			return root.Content[i-1]
		}
	}
	return &yaml.Node{Line: v.Line, Column: 1}
}

// compact reports whether the file writes this list's items flush with
// their key ("jobs:\n- name: …"), the common style for pipelines.
func (d *Document) compact(seq *yaml.Node) bool {
	if len(seq.Content) == 0 {
		return true
	}
	return seq.Content[0].Column-3 == d.keyOf(seq).Column-1
}

func indent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }
