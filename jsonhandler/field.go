package jsonhandler

import (
	"encoding/json/jsontext"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Field is one location in the value being edited, ready to render. The
// templates in this package walk a tree of Fields; README.md documents
// the markup each Kind produces.
type Field struct {
	Kind Kind

	// Path is the form path of the location (see the package
	// documentation) and Name the name of its value control.
	Path, Name string
	// Pointer is the JSON Pointer of the location in the JSON value; it
	// differs from Path inside maps, where Path addresses entries by
	// position.
	Pointer string
	// ID is the id of the field's control; derived ids add a suffix
	// (ID-description, ID-errors, …).
	ID string

	Label, Description string
	Placeholder        string
	Class              string

	// Required reports that the schema requires the value.
	// HTMLRequired reports that the control may carry the HTML required
	// attribute: the value is required and so is every enclosing value,
	// so the browser cannot block a submission the schema allows.
	Required, HTMLRequired bool
	ReadOnly, WriteOnly    bool
	Deprecated             bool

	// Type is the type attribute of an <input>.
	Type string
	// Value is the control's current value; Checked is a checkbox's.
	Value   string
	Checked bool
	Rows    int

	Min, Max, Step       string
	MinLength, MaxLength string
	Pattern              string

	Options []Option

	// Fields holds an object's properties or an array's items. Entries
	// holds a map's entries, or an object's properties that are not
	// declared under properties.
	Fields  []*Field
	Entries []*Field

	// Key is set on map entries: the control for the property name.
	Key *Key

	// Choice is the chosen variant of a KindChoice field. It is rendered
	// without a label of its own; the choice's legend labels it.
	Choice *Field
	// VariantName is the name of the variant picker.
	VariantName string

	// Collapsed reports an optional value that is left out; it renders
	// as a button that adds it.
	Collapsed bool
	// Removable reports an optional value that was added with its add
	// button and can be collapsed again.
	Removable bool
	// First and Last report an array item's or entry's position, for
	// move buttons.
	First, Last bool
	// CanAdd reports that an array or map may get another item or entry.
	CanAdd bool
	// LenName and Len carry the item or entry count.
	LenName string
	Len     int
	// PresentName is set when the value was explicitly included.
	PresentName string

	Errors []string

	nullable    bool
	jsonOptions bool
	format      string
	constant    jsontext.Value
	integer     bool
	defaultTrue bool
	variantOf   string
	variant     int
	explicit    bool
	form        *Form
}

// Option is a choice in a select, radio group or checkbox group.
type Option struct {
	Value, Label string
	Selected     bool
}

// Key is the property-name control of a map entry.
type Key struct {
	Name, ID, Value              string
	Pattern, MinLength, MaxLength string
	Errors                       []string
}

// Invalid reports that the field has errors to show.
func (f *Field) Invalid() bool { return len(f.Errors) > 0 }

// Action returns the value of an edit button that applies op to the field.
func (f *Field) Action(op string) string { return op + f.Path }

type builder struct {
	state     state
	idPrefix  string
	ancestors []*shape
	form      *Form
}

type site struct {
	path, pointer string
	label         string
	required      bool
	htmlRequired  bool
}

func (b *builder) build(sh *shape, at site, depth int) *Field {
	f := &Field{
		Path:         at.path,
		Name:         valueName(at.path),
		Pointer:      at.pointer,
		ID:           b.id(at.path),
		Label:        label(sh, at.label),
		Description:  sh.description,
		Class:        sh.ui.Class,
		Required:     at.required,
		HTMLRequired: at.required && at.htmlRequired,
		ReadOnly:     sh.readOnly,
		WriteOnly:    sh.writeOnly,
		Deprecated:   sh.deprecated,
		nullable:     sh.nullable(),
		format:       sh.format,
		form:         b.form,
	}
	f.Kind = sh.kind()
	if depth > maxDepth {
		f.Kind = KindJSON
	}
	switch f.Kind {
	case KindText, KindTextarea, KindNumber:
		b.scalar(f, sh)
	case KindCheckbox:
		raw, ok := b.state.value(at.path)
		f.defaultTrue = sh.def.Kind() == 't'
		if !ok {
			raw = strconv.FormatBool(f.defaultTrue)
		}
		f.Checked = raw == "true"
	case KindSelect, KindRadio:
		b.enum(f, sh)
	case KindCheckboxes:
		b.checkboxes(f, sh)
	case KindConst:
		f.constant = sh.constant
		f.Value = displayJSON(sh.constant)
	case KindNull:
	case KindJSON:
		f.Rows = max(sh.ui.Rows, 3)
		f.Placeholder = sh.ui.Placeholder
		b.initial(f, sh, indentJSON)
	case KindObject:
		b.object(f, sh, at, depth)
	case KindMap:
		b.entries(f, sh, at, depth)
	case KindArray:
		b.array(f, sh, at, depth)
	case KindChoice:
		b.choice(f, sh, at, depth)
	}
	return f
}

func (b *builder) scalar(f *Field, sh *shape) {
	f.Type = sh.inputType(f.Kind)
	f.Rows = max(sh.ui.Rows, 3)
	f.Placeholder = sh.ui.Placeholder
	if f.Placeholder == "" && len(sh.examples) > 0 {
		f.Placeholder = displayJSON(sh.examples[0])
	}
	b.initial(f, sh, func(v jsontext.Value) string {
		if f.Kind == KindNumber {
			return numberAttr(v)
		}
		s, _ := stringValue(v)
		return toControl(sh.format, s)
	})
	if f.Kind == KindNumber {
		b.numberLimits(f, sh)
		return
	}
	if n, ok := integerKeyword(sh.minLength); ok && n > 0 {
		f.MinLength = strconv.Itoa(n)
	}
	if n, ok := integerKeyword(sh.maxLength); ok {
		f.MaxLength = strconv.Itoa(n)
	}
	if sh.pattern != "" && sh.format == "" {
		// JSON Schema patterns match anywhere in the string; the HTML
		// pattern attribute must match all of it.
		f.Pattern = ".*(?:" + sh.pattern + ").*"
	}
}

// initial sets the control's value from the form state. A control the
// state does not mention yet starts from the schema default: a required
// value is filled in with it, while an optional one shows it as the
// placeholder, because leaving the control empty leaves the value out and
// the default applies anyway.
func (b *builder) initial(f *Field, sh *shape, format func(jsontext.Value) string) {
	if raw, ok := b.state.value(f.Path); ok {
		f.Value = raw
		return
	}
	if len(sh.def) == 0 {
		return
	}
	if f.Required {
		f.Value = format(sh.def)
		return
	}
	f.Placeholder = format(sh.def)
}

func (b *builder) numberLimits(f *Field, sh *shape) {
	f.integer = slices.Contains(sh.types, "integer") && !slices.Contains(sh.types, "number")
	f.Min, f.Max = numberAttr(sh.minimum), numberAttr(sh.maximum)
	if f.integer {
		if x, ok := number(sh.exclusiveMinimum); ok && f.Min == "" {
			f.Min = formatNumber(math.Floor(x) + 1)
		}
		if x, ok := number(sh.exclusiveMaximum); ok && f.Max == "" {
			f.Max = formatNumber(math.Ceil(x) - 1)
		}
	}
	f.Step = "any"
	if f.integer {
		f.Step = "1"
	}
	// The browser counts steps from min, so multipleOf only maps onto
	// step when min is itself a multiple (or absent).
	if m, ok := number(sh.multipleOf); ok && m > 0 && (!f.integer || m == math.Trunc(m)) {
		lo, hasMin := number(jsontext.Value(f.Min))
		if !hasMin || math.Mod(lo, m) == 0 {
			f.Step = numberAttr(sh.multipleOf)
		}
	}
}

func (b *builder) enum(f *Field, sh *shape) {
	f.jsonOptions = hasNonString(sh.enum)
	b.initial(f, sh, func(v jsontext.Value) string { return optionValue(v, f.jsonOptions) })
	raw := f.Value
	f.Options = make([]Option, 0, len(sh.enum)+1)
	if f.Kind == KindSelect || !f.Required {
		f.Options = append(f.Options, Option{Value: "", Label: f.Placeholder, Selected: raw == ""})
	}
	for _, v := range sh.enum {
		value := optionValue(v, f.jsonOptions)
		f.Options = append(f.Options, Option{Value: value, Label: displayJSON(v), Selected: value == raw})
	}
}

func (b *builder) checkboxes(f *Field, sh *shape) {
	items := flatten(sh.items...)
	f.jsonOptions = slices.ContainsFunc(items.enum, func(v jsontext.Value) bool { return v.Kind() != '"' })
	selected, ok := b.state.values(f.Path)
	if !ok {
		for _, v := range arrayElements(sh.def) {
			selected = append(selected, optionValue(v, f.jsonOptions))
		}
	}
	for _, v := range items.enum {
		value := optionValue(v, f.jsonOptions)
		f.Options = append(f.Options, Option{Value: value, Label: displayJSON(v), Selected: slices.Contains(selected, value)})
	}
	b.presence(f)
}

func optionValue(v jsontext.Value, jsonOptions bool) string {
	if s, ok := stringValue(v); ok && !jsonOptions {
		return s
	}
	return compactJSON(v)
}

func (b *builder) presence(f *Field) {
	f.explicit = b.state.present(f.Path)
	if f.explicit {
		f.PresentName = metaName(metaPresent, f.Path)
	}
}

func (b *builder) object(f *Field, sh *shape, at site, depth int) {
	b.presence(f)
	b.ancestors = append(b.ancestors, sh)
	defer func() { b.ancestors = b.ancestors[:len(b.ancestors)-1] }()
	for _, p := range sh.orderedProperties() {
		child := flatten(p.nodes...)
		if child.never {
			continue
		}
		required := sh.required[p.name]
		childAt := site{
			path:         propertyPath(at.path, p.name),
			pointer:      propertyPath(at.pointer, p.name),
			label:        p.name,
			required:     required,
			htmlRequired: f.HTMLRequired,
		}
		if !required && b.collapses(child, childAt.path) {
			f.Fields = append(f.Fields, b.collapsed(child, childAt))
			continue
		}
		cf := b.build(child, childAt, depth+1)
		cf.Removable = !required && cf.explicit && b.collapsible(child)
		f.Fields = append(f.Fields, cf)
	}
	if n, ok := b.state.count(at.path); (ok && n > 0) || (len(sh.additional) > 0 && !sh.closed) {
		b.entries(f, sh, at, depth)
	}
}

// collapsible reports whether an optional value starts out left out
// rather than shown empty: deprecated keywords, values whose schema
// repeats an enclosing schema (which would otherwise expand forever), and
// anything annotated with "collapsed": true.
func (b *builder) collapsible(sh *shape) bool {
	if sh.ui.Collapsed != nil {
		return *sh.ui.Collapsed
	}
	if sh.deprecated {
		return true
	}
	switch sh.kind() {
	case KindObject, KindMap, KindArray, KindChoice, KindJSON:
		return sh.recursiveWith(b.ancestors)
	}
	return false
}

func (b *builder) collapses(sh *shape, path string) bool {
	return b.collapsible(sh) && !b.state.touched(path)
}

func (b *builder) collapsed(sh *shape, at site) *Field {
	return &Field{
		Kind:        sh.kind(),
		Path:        at.path,
		Name:        valueName(at.path),
		Pointer:     at.pointer,
		ID:          b.id(at.path),
		Label:       label(sh, at.label),
		Description: sh.description,
		Class:       sh.ui.Class,
		Deprecated:  sh.deprecated,
		Collapsed:   true,
		form:        b.form,
	}
}

func (b *builder) entries(f *Field, sh *shape, at site, depth int) {
	b.presence(f)
	n, _ := b.state.count(at.path)
	f.LenName, f.Len = metaName(metaLen, at.path), n
	maxProps, hasMax := integerKeyword(sh.maxProps)
	f.CanAdd = !sh.closed && (!hasMax || n < maxProps) && n < maxItems
	values := sh.additional
	if len(values) == 0 {
		values = []node{{}} // no schema: any JSON value
	}
	names := flatten(sh.names...)
	b.ancestors = append(b.ancestors, sh)
	defer func() { b.ancestors = b.ancestors[:len(b.ancestors)-1] }()
	for i := range n {
		path := entryPath(at.path, i)
		key, _ := b.state.meta(metaKey, path)
		k := &Key{Name: metaName(metaKey, path), ID: b.id(path) + "-key", Value: key}
		if names.pattern != "" {
			k.Pattern = ".*(?:" + names.pattern + ").*"
		}
		if v, ok := integerKeyword(names.minLength); ok && v > 0 {
			k.MinLength = strconv.Itoa(v)
		}
		if v, ok := integerKeyword(names.maxLength); ok {
			k.MaxLength = strconv.Itoa(v)
		}
		entry := b.build(flatten(values...), site{
			path:         path,
			pointer:      propertyPath(at.pointer, key),
			label:        firstNonEmpty(key, "Value"),
			required:     true,
			htmlRequired: true,
		}, depth+1)
		entry.Key = k
		entry.Label = firstNonEmpty(key, "Value")
		entry.First, entry.Last = i == 0, i == n-1
		f.Entries = append(f.Entries, entry)
	}
}

func (b *builder) array(f *Field, sh *shape, at site, depth int) {
	b.presence(f)
	n, ok := b.state.count(at.path)
	if !ok {
		minimum, _ := integerKeyword(sh.minItems)
		if !at.required {
			minimum = 0
		}
		n = min(max(minimum, len(sh.prefixItems)), maxItems)
	}
	f.LenName, f.Len = metaName(metaLen, at.path), n
	maximum, hasMax := integerKeyword(sh.maxItems)
	itemsClosed := len(sh.items) > 0 && flatten(sh.items...).never
	f.CanAdd = (!hasMax || n < maximum) && n < maxItems && (!itemsClosed || n < len(sh.prefixItems))
	b.ancestors = append(b.ancestors, sh)
	defer func() { b.ancestors = b.ancestors[:len(b.ancestors)-1] }()
	for i := range n {
		item := flatten(sh.itemNodes(i)...)
		path := itemPath(at.path, i)
		cf := b.build(item, site{
			path:         path,
			pointer:      itemPath(at.pointer, i),
			label:        ordinal(f.Label, i),
			required:     true,
			htmlRequired: true,
		}, depth+1)
		cf.Label = ordinal(firstNonEmpty(item.ui.Label, item.ownTitle, f.Label), i)
		cf.First, cf.Last = i == 0, i == n-1
		f.Fields = append(f.Fields, cf)
	}
}

func (b *builder) choice(f *Field, sh *shape, at site, depth int) {
	choices := sh.choices()
	f.VariantName = metaName(metaVariant, at.path)
	f.variantOf = sh.variantOf
	idx := pick(choices, sh.def)
	if raw, ok := b.state.meta(metaVariant, at.path); ok {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n < len(choices) {
			idx = n
		}
	}
	f.variant = idx
	for i, c := range choices {
		f.Options = append(f.Options, Option{Value: strconv.Itoa(i), Label: c.label, Selected: i == idx})
	}
	if len(choices) == 0 {
		f.Kind = KindJSON
		return
	}
	chosen := b.build(choices[idx].shape, site{
		path:         at.path,
		pointer:      at.pointer,
		label:        f.Label,
		required:     at.required,
		htmlRequired: at.htmlRequired,
	}, depth+1)
	chosen.ID = f.ID + "-value"
	chosen.Label = firstNonEmpty(choices[idx].shape.ui.Label, choices[idx].label)
	f.Choice = chosen
}

// id derives an element id from a form path. Letters, digits and "-" are
// kept, "/" becomes ".", and every other byte is written as "_" and two
// hex digits, so distinct paths always get distinct ids.
func (b *builder) id(path string) string {
	const hex = "0123456789abcdef"
	var sb strings.Builder
	sb.WriteString(b.idPrefix)
	sb.WriteByte('-')
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-':
			sb.WriteByte(c)
		case c == '/':
			sb.WriteByte('.')
		default:
			sb.WriteByte('_')
			sb.WriteByte(hex[c>>4])
			sb.WriteByte(hex[c&0xf])
		}
	}
	return sb.String()
}

// label names a location: the x-form label, else the schema's own title,
// else the property name, else a title inherited through a reference.
func label(sh *shape, name string) string {
	return firstNonEmpty(sh.ui.Label, sh.ownTitle, name, sh.title)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
