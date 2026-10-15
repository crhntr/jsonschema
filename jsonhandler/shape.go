package jsonhandler

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/jsonptr"
)

// node is a subschema together with the dynamic scope it is evaluated in.
// The scope is the list of resource roots entered on the way to the
// subschema, outermost first; $dynamicRef resolves against it.
type node struct {
	schema *jsonschema.Schema
	scope  scope
}

type scope []*jsonschema.Schema

// push returns the scope with resource appended, unless resource is
// already the innermost entry.
func (sc scope) push(resource *jsonschema.Schema) scope {
	if resource == nil || (len(sc) > 0 && sc[len(sc)-1] == resource) {
		return sc
	}
	return append(slices.Clip(sc), resource)
}

// dynamic resolves the $dynamicRef on s: the outermost resource in the
// scope with a matching $dynamicAnchor wins (JSON Schema 2020-12
// §8.2.3.2). A $dynamicRef that is not bookended behaves like $ref.
func (sc scope) dynamic(s *jsonschema.Schema, ref string) *jsonschema.Schema {
	if s.IsDynamic() {
		_, name, _ := strings.Cut(ref, "#")
		for _, resource := range sc {
			if target := resource.DynamicAnchor(name); target != nil {
				return target
			}
		}
	}
	return s.Resolved()
}

// AnnotationKeyword is the schema keyword holding form annotations.
const AnnotationKeyword = "x-form"

// annotations is the value of the x-form keyword. Every member is
// optional; README.md describes what each one does.
type annotations struct {
	Widget      string   `json:"widget,omitempty"`
	Label       string   `json:"label,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Order       []string `json:"order,omitempty"`
	Class       string   `json:"class,omitempty"`
	Collapsed   *bool    `json:"collapsed,omitempty"`
	Rows        int      `json:"rows,omitempty"`
	// Messages replace the validator's message for a failing keyword,
	// such as "pattern" or "required".
	Messages map[string]string `json:"messages,omitempty"`
}

type property struct {
	name  string
	nodes []node
}

// shape is a schema flattened for form building: $ref and $dynamicRef are
// followed and allOf members merged, so one shape describes everything
// the form needs to know about a location. For single-valued keywords the
// schema's own value wins over one reached through $ref, which wins over
// one from an allOf member.
type shape struct {
	members []*jsonschema.Schema

	never bool // the false schema: nothing is valid

	title, description string
	ownTitle           string // the title on the schema itself, not reached by reference
	refName            string

	types    []jsonschema.SimpleType
	hasTypes bool

	properties []property
	required   map[string]bool

	additional []node // explicit additionalProperties; nil when absent
	closed     bool   // additionalProperties: false
	names      []node // propertyNames

	items       []node
	prefixItems [][]node

	enum      []jsontext.Value
	constant  jsontext.Value
	def       jsontext.Value
	examples  []jsontext.Value
	variants  []node
	variantOf string // "oneOf" or "anyOf"

	format, pattern, contentMediaType      string
	minLength, maxLength                   jsontext.Value
	minimum, maximum                       jsontext.Value
	exclusiveMinimum, exclusiveMaximum     jsontext.Value
	multipleOf                             jsontext.Value
	minItems, maxItems, minProps, maxProps jsontext.Value
	uniqueItems, readOnly, writeOnly       bool
	deprecated                             bool

	ui annotations
}

func flatten(nodes ...node) *shape {
	sh := &shape{required: map[string]bool{}}
	seen := map[*jsonschema.Schema]bool{}
	for _, n := range nodes {
		if n.schema != nil {
			if obj, ok := n.schema.TypeObject(); ok {
				setString(&sh.ownTitle, obj.Title)
			}
		}
		sh.merge(n, seen)
	}
	sh.booleanOptions()
	return sh
}

// booleanOptions lets a boolean annotated with the select or radio widget
// render as a choice between true and false, which, unlike a checkbox,
// can also leave the value out.
func (sh *shape) booleanOptions() {
	if sh.enum == nil && (sh.ui.Widget == "select" || sh.ui.Widget == "radio") && slices.Equal(sh.nonNullTypes(), []jsonschema.SimpleType{"boolean"}) {
		sh.enum = []jsontext.Value{jsontext.Value("true"), jsontext.Value("false")}
	}
}

func (sh *shape) merge(n node, seen map[*jsonschema.Schema]bool) {
	s := n.schema
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	if b, ok := s.TypeBoolean(); ok {
		sh.never = sh.never || !b
		return
	}
	obj, _ := s.TypeObject()
	sh.members = append(sh.members, s)
	sc := n.scope.push(s.Resource())
	sh.mergeKeywords(s, obj, sc)
	if obj.Ref != "" {
		setString(&sh.refName, refName(obj.Ref))
		sh.merge(node{schema: s.Resolved(), scope: sc}, seen)
	}
	if obj.DynamicRef != "" {
		sh.merge(node{schema: sc.dynamic(s, obj.DynamicRef), scope: sc}, seen)
	}
	for _, member := range obj.AllOf {
		sh.merge(node{schema: member, scope: sc}, seen)
	}
}

func (sh *shape) mergeKeywords(s *jsonschema.Schema, obj jsonschema.SchemaObject, sc scope) {
	setString(&sh.title, obj.Title)
	setString(&sh.description, obj.Description)
	setString(&sh.format, obj.Format)
	setString(&sh.pattern, obj.Pattern)
	setString(&sh.contentMediaType, obj.ContentMediaType)
	setValue(&sh.constant, obj.Const)
	setValue(&sh.def, obj.Default)
	setValue(&sh.minLength, obj.MinLength)
	setValue(&sh.maxLength, obj.MaxLength)
	setValue(&sh.minimum, obj.Minimum)
	setValue(&sh.maximum, obj.Maximum)
	setValue(&sh.exclusiveMinimum, obj.ExclusiveMinimum)
	setValue(&sh.exclusiveMaximum, obj.ExclusiveMaximum)
	setValue(&sh.multipleOf, obj.MultipleOf)
	setValue(&sh.minItems, obj.MinItems)
	setValue(&sh.maxItems, obj.MaxItems)
	setValue(&sh.minProps, obj.MinProperties)
	setValue(&sh.maxProps, obj.MaxProperties)
	if sh.enum == nil {
		sh.enum = obj.Enum
	}
	if sh.examples == nil {
		sh.examples = obj.Examples
	}
	sh.uniqueItems = sh.uniqueItems || obj.UniqueItems
	sh.readOnly = sh.readOnly || obj.ReadOnly
	sh.writeOnly = sh.writeOnly || obj.WriteOnly
	sh.deprecated = sh.deprecated || obj.Deprecated
	if obj.Type != nil {
		sh.intersectTypes(typeList(obj.Type))
	}
	for _, name := range obj.Required {
		sh.required[name] = true
	}
	for _, name := range propertyOrder(s, obj) {
		sh.addProperty(name, node{schema: obj.Properties[name], scope: sc})
	}
	if obj.AdditionalProperties != nil {
		if b, ok := obj.AdditionalProperties.TypeBoolean(); ok && !b {
			sh.closed = true
		} else {
			sh.additional = append(sh.additional, node{schema: obj.AdditionalProperties, scope: sc})
		}
	}
	if obj.PropertyNames != nil {
		sh.names = append(sh.names, node{schema: obj.PropertyNames, scope: sc})
	}
	if obj.Items != nil {
		sh.items = append(sh.items, node{schema: obj.Items, scope: sc})
	}
	for i, item := range obj.PrefixItems {
		if i == len(sh.prefixItems) {
			sh.prefixItems = append(sh.prefixItems, nil)
		}
		sh.prefixItems[i] = append(sh.prefixItems[i], node{schema: item, scope: sc})
	}
	if sh.variants == nil {
		switch {
		case len(obj.OneOf) > 0:
			sh.variantOf = "oneOf"
			sh.variants = schemaNodes(obj.OneOf, sc)
		case len(obj.AnyOf) > 0:
			sh.variantOf = "anyOf"
			sh.variants = schemaNodes(obj.AnyOf, sc)
		}
	}
	if raw, ok := obj.Extra[AnnotationKeyword]; ok {
		sh.mergeAnnotations(raw)
	}
}

// mergeAnnotations reads x-form. A malformed annotation is ignored rather
// than failing the form: annotations only adjust presentation, and the
// schema still describes the data.
func (sh *shape) mergeAnnotations(raw jsontext.Value) {
	var ui annotations
	if err := json.Unmarshal(raw, &ui); err != nil {
		return
	}
	setString(&sh.ui.Widget, ui.Widget)
	setString(&sh.ui.Label, ui.Label)
	setString(&sh.ui.Placeholder, ui.Placeholder)
	setString(&sh.ui.Class, ui.Class)
	if sh.ui.Order == nil {
		sh.ui.Order = ui.Order
	}
	if sh.ui.Collapsed == nil {
		sh.ui.Collapsed = ui.Collapsed
	}
	if sh.ui.Rows == 0 {
		sh.ui.Rows = ui.Rows
	}
	for keyword, message := range ui.Messages {
		if sh.ui.Messages == nil {
			sh.ui.Messages = map[string]string{}
		}
		if _, ok := sh.ui.Messages[keyword]; !ok {
			sh.ui.Messages[keyword] = message
		}
	}
}

func (sh *shape) addProperty(name string, n node) {
	for i := range sh.properties {
		if sh.properties[i].name == name {
			sh.properties[i].nodes = append(sh.properties[i].nodes, n)
			return
		}
	}
	sh.properties = append(sh.properties, property{name: name, nodes: []node{n}})
}

// intersectTypes narrows the allowed types to those both schemas allow.
// integer is a subset of number, so number ∩ integer is integer.
func (sh *shape) intersectTypes(types []jsonschema.SimpleType) {
	if !sh.hasTypes {
		sh.hasTypes = true
		sh.types = types
		return
	}
	var both []jsonschema.SimpleType
	for _, t := range sh.types {
		switch {
		case slices.Contains(types, t):
			both = append(both, t)
		case t == "integer" && slices.Contains(types, "number"),
			t == "number" && slices.Contains(types, "integer"):
			both = append(both, "integer")
		}
	}
	sh.types = both
}

// withVariant returns a copy of sh with the variant merged in. Keywords
// next to oneOf/anyOf apply to every variant, so each variant starts from
// the parent.
func (sh *shape) withVariant(v node) *shape {
	c := *sh
	c.members = slices.Clone(sh.members)
	c.types = slices.Clone(sh.types)
	c.properties = slices.Clone(sh.properties)
	c.required = maps.Clone(sh.required)
	c.additional = slices.Clone(sh.additional)
	c.items = slices.Clone(sh.items)
	c.prefixItems = slices.Clone(sh.prefixItems)
	c.variants = nil
	c.variantOf = ""
	seen := map[*jsonschema.Schema]bool{}
	for _, m := range sh.members {
		seen[m] = true
	}
	// The variant describes itself better than the parent does.
	c.description = ""
	c.merge(v, seen)
	setString(&c.description, sh.description)
	c.booleanOptions()
	return &c
}

// withType returns a copy of sh restricted to one type.
func (sh *shape) withType(t jsonschema.SimpleType) *shape {
	c := *sh
	c.types = []jsonschema.SimpleType{t}
	c.variants = nil
	c.variantOf = ""
	return &c
}

// orderedProperties returns the properties in x-form order: names listed
// in the order annotation first, then the rest in schema source order.
func (sh *shape) orderedProperties() []property {
	if len(sh.ui.Order) == 0 {
		return sh.properties
	}
	out := make([]property, 0, len(sh.properties))
	for _, name := range sh.ui.Order {
		if p, ok := sh.property(name); ok && !slices.ContainsFunc(out, func(q property) bool { return q.name == name }) {
			out = append(out, p)
		}
	}
	for _, p := range sh.properties {
		if !slices.Contains(sh.ui.Order, p.name) {
			out = append(out, p)
		}
	}
	return out
}

func (sh *shape) property(name string) (property, bool) {
	i := slices.IndexFunc(sh.properties, func(p property) bool { return p.name == name })
	if i < 0 {
		return property{}, false
	}
	return sh.properties[i], true
}

func (sh *shape) nullable() bool { return slices.Contains(sh.types, "null") }

// nonNullTypes returns the declared types other than null.
func (sh *shape) nonNullTypes() []jsonschema.SimpleType {
	return slices.DeleteFunc(slices.Clone(sh.types), func(t jsonschema.SimpleType) bool { return t == "null" })
}

// itemNodes returns the schema for array item i.
func (sh *shape) itemNodes(i int) []node {
	if i < len(sh.prefixItems) {
		return sh.prefixItems[i]
	}
	return sh.items
}

// recursiveWith reports whether sh shares a schema with an enclosing
// shape, meaning that expanding it could repeat forever.
func (sh *shape) recursiveWith(ancestors []*shape) bool {
	for _, a := range ancestors {
		for _, m := range sh.members {
			if slices.Contains(a.members, m) {
				return true
			}
		}
	}
	return false
}

func schemaNodes(schemas []*jsonschema.Schema, sc scope) []node {
	out := make([]node, len(schemas))
	for i, s := range schemas {
		out[i] = node{schema: s, scope: sc}
	}
	return out
}

func setString(dst *string, src string) {
	if *dst == "" {
		*dst = src
	}
}

func setValue(dst *jsontext.Value, src jsontext.Value) {
	if len(*dst) == 0 {
		*dst = src
	}
}

func typeList(t *jsonschema.Type) []jsonschema.SimpleType {
	if s, ok := t.TypeString(); ok {
		return []jsonschema.SimpleType{s}
	}
	a, _ := t.TypeArray()
	return slices.Clone(a)
}

// refName returns the last reference token of a $ref, used as a variant
// label when the target has no title: "#/$defs/address" → "address".
func refName(ref string) string {
	_, fragment, found := strings.Cut(ref, "#")
	if !found {
		fragment = ref
	}
	name := fragment[strings.LastIndexByte(fragment, '/')+1:]
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(name)
}

// propertyOrder returns the keys of obj.Properties in the order they appear
// in the schema document. Go maps lose that order, so the keys are read
// back from the resource source; any key the source does not account for
// (for example in an embedded resource) is appended in sorted order.
func propertyOrder(s *jsonschema.Schema, obj jsonschema.SchemaObject) []string {
	if len(obj.Properties) == 0 {
		return nil
	}
	var order []string
	if resource := s.Resource(); resource != nil {
		ptr := jsonptr.Pointer(s.PathInResource()).Append("properties")
		if raw, err := jsonptr.Find(resource.Source(), ptr); err == nil {
			order = objectKeys(raw)
		}
	}
	order = slices.DeleteFunc(order, func(k string) bool {
		_, ok := obj.Properties[k]
		return !ok
	})
	order = slices.Compact(order)
	var rest []string
	for k := range obj.Properties {
		if !slices.Contains(order, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(order, rest...)
}

// objectKeys returns the member names of a JSON object in document order,
// or nil when raw is not an object.
func objectKeys(raw jsontext.Value) []string {
	dec := jsontext.NewDecoder(strings.NewReader(string(raw)))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil
	}
	var keys []string
	for dec.PeekKind() == '"' {
		tok, err := dec.ReadToken()
		if err != nil {
			return keys
		}
		keys = append(keys, tok.String())
		if err := dec.SkipValue(); err != nil {
			return keys
		}
	}
	return keys
}

// number parses a numeric keyword value. ok is false when the keyword is
// absent or not a number.
func number(v jsontext.Value) (float64, bool) {
	if len(v) == 0 || v.Kind() != '0' {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
	return f, err == nil
}
