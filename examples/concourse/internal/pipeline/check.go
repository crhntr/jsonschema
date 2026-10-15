package pipeline

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/jsonptr"
)

// Problem is one reason the pipeline does not match its schema, placed on
// the line of the YAML it is about.
type Problem struct {
	Pointer      string
	Line, Column int
	Message      string
}

// Checker checks pipelines against a schema and words the failures for
// people reading YAML.
type Checker struct {
	schema *jsonschema.Schema
	doc    jsontext.Value // the schema document, to look up branches and descriptions
}

// NewChecker returns a Checker for a resolved schema and its document.
func NewChecker(schema *jsonschema.Schema, doc []byte) *Checker {
	return &Checker{schema: schema, doc: doc}
}

// Check returns the problems in d, in file order.
func (c *Checker) Check(d *Document) []Problem {
	out := c.schema.Validate("pipeline", d.JSON())
	if out.Valid {
		return nil
	}
	var problems []Problem
	c.collect(out, d, &problems)
	problems = c.dropUnevaluatedNoise(problems)
	slices.SortStableFunc(problems, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
	return slices.CompactFunc(problems, func(a, b Problem) bool { return a == b })
}

// collect walks the verbose output. At a oneOf or anyOf it follows only
// the branches that apply to the value, so a get step is reported as a get
// step and not as a failed put, task and so on.
func (c *Checker) collect(o jsonschema.Output, d *Document, problems *[]Problem) {
	if o.Valid {
		return
	}
	switch last := lastToken(o.KeywordLocation); last {
	case "oneOf", "anyOf":
		applies, names, ok := c.branches(o, d)
		if !ok {
			break // report every branch, as the validator does
		}
		if len(applies) == 0 {
			*problems = append(*problems, c.problem(d, o.InstanceLocation, "expected one of: "+strings.Join(names, ", ")))
			return
		}
		for _, child := range o.Errors {
			if slices.Contains(applies, lastToken(child.KeywordLocation)) {
				c.collect(child, d, problems)
			}
		}
		return
	}
	var failing bool
	for _, child := range o.Errors {
		if !child.Valid {
			failing = true
			c.collect(child, d, problems)
		}
	}
	if !failing && o.Error != "" {
		*problems = append(*problems, c.problem(d, o.InstanceLocation, c.message(o, d)))
	}
}

func (c *Checker) problem(d *Document, ptr, msg string) Problem {
	pos := d.Position(ptr)
	return Problem{Pointer: ptr, Line: pos.Line, Column: pos.Column, Message: msg}
}

// message rewords the validator's messages that read poorly next to YAML.
func (c *Checker) message(o jsonschema.Output, d *Document) string {
	switch lastToken(o.KeywordLocation) {
	case "additionalProperties", "unevaluatedProperties":
		return fmt.Sprintf("unknown field %q", lastToken(o.InstanceLocation))
	case "pattern":
		// The definition's description says what the pattern means.
		if desc := c.describe(parent(o.AbsoluteKeywordLocation)); desc != "" {
			return "expected " + lowerFirst(strings.TrimSuffix(desc, "."))
		}
	case "type":
		var want any
		if err := c.lookup(o.AbsoluteKeywordLocation, &want); err == nil && d != nil {
			if got, err := d.Value(o.InstanceLocation); err == nil {
				return fmt.Sprintf("expected %s, got %s", typeList(want), jsonType(got))
			}
		}
	}
	return o.Error
}

func typeList(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var names []string
		for _, n := range t {
			names = append(names, fmt.Sprint(n))
		}
		return strings.Join(names, " or ")
	}
	return fmt.Sprint(v)
}

// branches returns the indexes (as tokens) of the branches of a oneOf or
// anyOf that apply to the value, judged by each branch's type and the
// first property it requires, and a name for every branch. ok is false
// when the branches cannot be found in the schema document.
func (c *Checker) branches(o jsonschema.Output, d *Document) (applies, names []string, ok bool) {
	var list []map[string]jsontext.Value
	if err := c.lookup(o.AbsoluteKeywordLocation, &list); err != nil {
		return nil, nil, false
	}
	value, err := d.Value(o.InstanceLocation)
	if err != nil {
		return nil, nil, false
	}
	kind := jsonType(value)
	for i, branch := range list {
		branch = c.deref(branch)
		var title, typ string
		var required []string
		for key, dst := range map[string]any{"title": &title, "type": &typ, "required": &required} {
			if raw, ok := branch[key]; ok {
				if err := json.Unmarshal(raw, dst); err != nil {
					continue // a type list or other shape: judged as not narrowing
				}
			}
		}
		name := cmp.Or(title, first(required), typ, fmt.Sprint(i))
		names = append(names, name)
		if typ != "" && typ != kind && !(typ == "number" && kind == "integer") {
			continue
		}
		if len(required) > 0 && kind == "object" {
			if _, err := jsonptr.Find(value, jsonptr.Pointer("").Append(required[0])); err != nil {
				continue
			}
		}
		applies = append(applies, fmt.Sprint(i))
	}
	return applies, names, true
}

// dropUnevaluatedNoise removes "unknown field" reports for a field that
// has a problem of its own: a failing subschema does not count as having
// evaluated the field, so the validator reports both.
func (c *Checker) dropUnevaluatedNoise(problems []Problem) []Problem {
	real := map[string]bool{}
	for _, p := range problems {
		if !strings.HasPrefix(p.Message, "unknown field ") {
			real[p.Pointer] = true
		}
	}
	return slices.DeleteFunc(problems, func(p Problem) bool {
		return strings.HasPrefix(p.Message, "unknown field ") && real[p.Pointer]
	})
}

// lookup decodes the schema at an absolute keyword location into dst.
func (c *Checker) lookup(location string, dst any) error {
	_, frag, ok := strings.Cut(location, "#")
	if !ok {
		return fmt.Errorf("no fragment in %q", location)
	}
	raw, err := jsonptr.Find(c.doc, jsonptr.Pointer(frag))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// deref follows a branch's local $ref once.
func (c *Checker) deref(branch map[string]jsontext.Value) map[string]jsontext.Value {
	var ref string
	if raw, ok := branch["$ref"]; !ok || json.Unmarshal(raw, &ref) != nil || !strings.HasPrefix(ref, "#") {
		return branch
	}
	var target map[string]jsontext.Value
	if err := c.lookup(ref, &target); err != nil {
		return branch
	}
	return target
}

func (c *Checker) describe(location string) string {
	var node struct {
		Description string `json:"description"`
	}
	if err := c.lookup(location, &node); err != nil {
		return ""
	}
	return node.Description
}

func jsonType(v jsontext.Value) string {
	switch v.Kind() {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	if strings.ContainsAny(string(v), ".eE") {
		return "number"
	}
	return "integer"
}

func lastToken(ptr string) string {
	return ptr[strings.LastIndexByte(ptr, '/')+1:]
}

func parent(ptr string) string {
	return ptr[:max(strings.LastIndexByte(ptr, '/'), 0)]
}

func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
