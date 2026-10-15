// Package pipeline reads, checks and edits a Concourse pipeline file. The
// YAML is converted to JSON to be checked against the pipeline schema, and
// edits made as JSON are written back into the YAML in place.
package pipeline

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/crhntr/jsonschema/jsonptr"

	"github.com/crhntr/jsonschema/examples/concourse/internal/yamljson"
)

// Document is a pipeline file as YAML text, and as the JSON it stands for.
type Document struct {
	src  []byte
	node *yaml.Node
	json jsontext.Value
	pos  yamljson.Positions
}

// Parse reads a pipeline file.
func Parse(src []byte) (*Document, error) {
	node, err := yamljson.Parse(src)
	if err != nil {
		return nil, err
	}
	v, pos, err := yamljson.Decode(node)
	if err != nil {
		return nil, err
	}
	return &Document{src: src, node: node, json: v, pos: pos}, nil
}

// JSON returns the pipeline as JSON.
func (d *Document) JSON() jsontext.Value { return d.json }

// YAML returns the pipeline file. Edits change only the lines of the
// sections they touch; everything else is the text as it was written.
func (d *Document) YAML() ([]byte, error) { return d.src, nil }

// Position returns where the value at ptr is written.
func (d *Document) Position(ptr string) yamljson.Pos { return d.pos.At(ptr) }

// Value returns the JSON value at ptr.
func (d *Document) Value(ptr string) (jsontext.Value, error) {
	return jsonptr.Find(d.json, jsonptr.Pointer(ptr))
}

// Kind is a top-level list of named things in a pipeline.
type Kind string

const (
	Jobs          Kind = "jobs"
	Resources     Kind = "resources"
	ResourceTypes Kind = "resource_types"
)

// Definition is the name of the schema definition for one item of k.
func (k Kind) Definition() string {
	switch k {
	case Jobs:
		return "job"
	case Resources:
		return "resource"
	}
	return "resource_type"
}

// Section is one named item in a top-level list.
type Section struct {
	Kind    Kind
	Name    string
	Pointer string
	Line    int
}

// Sections lists the named items of k in file order.
func (d *Document) Sections(k Kind) []Section {
	var items []jsontext.Value
	raw, err := d.Value("/" + string(k))
	if err != nil || json.Unmarshal(raw, &items) != nil {
		return nil // a missing or malformed list is the schema's to report
	}
	var out []Section
	for i, item := range items {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(item, &named); err != nil {
			named.Name = "" // the schema reports items that are not objects
		}
		ptr := "/" + string(k) + "/" + strconv.Itoa(i)
		out = append(out, Section{Kind: k, Name: named.Name, Pointer: ptr, Line: d.Position(ptr).Line})
	}
	return out
}

// Section finds the item of k with the name.
func (d *Document) Section(k Kind, name string) (Section, bool) {
	sections := d.Sections(k)
	i := slices.IndexFunc(sections, func(s Section) bool { return s.Name == name })
	if i < 0 {
		return Section{}, false
	}
	return sections[i], true
}
