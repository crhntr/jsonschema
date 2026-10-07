package jsonhandler

import (
	"slices"
	"strconv"
)

// view is what the templates render, derived from a form after its errors
// are attached: a copy of the fields in which hidden groups (and, in the
// wizard, every group below the current step) are stubs, plus the
// controls the copy does not render, carried as hidden inputs so the page
// holds the whole form state.
type view struct {
	root    *Field
	hidden  []Control
	wizard  *Wizard
	outline []OutlineItem
}

// Wizard is what the wizard shows around the current step.
type Wizard struct {
	// Step is the form path of the group being edited.
	Step string
	// Crumbs lead from the root group to the step, which is last.
	Crumbs []Crumb
	// Back, Up and Next are the previous group in document order, the
	// enclosing group, and the next group; nil where there is none.
	Back, Up, Next *Crumb
	// Elsewhere lists the other groups with errors of their own.
	Elsewhere []Crumb
}

// Crumb names a group the wizard can go to.
type Crumb struct {
	Label, Path string
	Current     bool
}

// Action is the value of the edit button that goes to the group.
func (c Crumb) Action() string { return ActionWizard + c.Path }

// OutlineItem is an entry in the form's outline for an object, array, map
// or choice, with the groups inside it.
type OutlineItem struct {
	Label, Href, Path string
	// Note is the chosen variant of a choice.
	Note string
	// Invalid reports errors in the group or anywhere inside it.
	Invalid bool
	// Folded reports a hidden group; its items are left out.
	Folded bool
	// Wizard reports that the form is in the wizard, where entries go to
	// the group's step instead of linking to it, and Current marks the
	// step.
	Wizard, Current bool
	Items           []OutlineItem
}

// FoldAction is the value of the button that hides or shows the group.
func (o OutlineItem) FoldAction() string {
	if o.Folded {
		return "unfold" + o.Path
	}
	return "fold" + o.Path
}

// StepAction is the value of the button that edits the group in the
// wizard.
func (o OutlineItem) StepAction() string { return ActionWizard + o.Path }

// View returns the field to render: the root, or the wizard's step.
func (f *Form) View() *Field { return f.prepared().root }

// Hidden returns the controls outside the rendered fields, to be carried
// as hidden inputs.
func (f *Form) Hidden() []Control { return f.prepared().hidden }

// Wizard returns the wizard's navigation, or nil outside the wizard.
func (f *Form) Wizard() *Wizard { return f.prepared().wizard }

// Outline returns the groups in the form, nested as they are.
func (f *Form) Outline() []OutlineItem { return f.prepared().outline }

// AllFields returns every rendered field with an error list, outermost
// first.
func (f *Form) AllFields() []*Field {
	var out []*Field
	var walk func(*Field)
	walk = func(field *Field) {
		if field.Collapsed || field.Stub {
			return
		}
		out = append(out, field)
		for _, c := range field.Fields {
			walk(c)
		}
		for _, c := range field.Entries {
			walk(c)
		}
		if field.Choice != nil {
			walk(field.Choice)
		}
	}
	if root := f.View(); root != nil {
		walk(root)
	}
	return out
}

func (f *Form) prepared() *view {
	if f.view == nil {
		f.view = f.prepare()
	}
	return f.view
}

func (f *Form) prepare() *view {
	v := &view{}
	if f.Root == nil {
		return v
	}
	var secret []string
	for _, field := range f.fields(func(field *Field) bool { return field.WriteOnly }) {
		secret = append(secret, field.Path)
	}
	groups := groupsOf(f.Root)
	step := -1
	if f.Mode == ModeWizard {
		step = slices.IndexFunc(groups, func(g groupRef) bool { return g.field.Path == f.step })
		if step < 0 && len(groups) > 0 {
			step = 0
		}
	}
	if step < 0 {
		v.root = f.trim(f.Root, true, func(g *Field) bool { return f.folded(g.Path) }, secret)
	} else {
		at := groups[step].field
		v.root = f.trim(at, true, isGroup, secret)
		keyName := metaName(metaKey, at.Path)
		v.hidden = f.controls(func(name, path string) bool {
			return !within(path, at.Path) || name == keyName
		}, secret)
		v.wizard = wizardFor(groups, step)
	}
	if f.Mode == ModeWizard && step < 0 {
		// A value with no groups is its own only step.
		v.wizard = &Wizard{Crumbs: []Crumb{{Label: firstNonEmpty(f.Root.Label, "Value"), Current: true}}}
	}
	v.outline = f.outline(f.Root, v.wizard)
	return v
}

func (f *Form) folded(path string) bool {
	_, ok := f.state.meta(metaFolded, path)
	return ok
}

// trim copies field for rendering. Each group below it that stub reports
// becomes a stub carrying its controls as hidden inputs. A choice and its
// chosen variant share a location, so the variant is never a stub of its
// own choice.
func (f *Form) trim(field *Field, top bool, stub func(*Field) bool, secret []string) *Field {
	c := *field
	if !top && isGroup(field) && stub(field) {
		c.Stub = true
		c.Folded = f.folded(field.Path)
		c.StubInvalid = field.invalidWithin()
		c.Summary = summary(field)
		c.Fields, c.Entries, c.Choice = nil, nil, nil
		keyName := metaName(metaKey, field.Path)
		c.Hidden = f.controls(func(name, path string) bool {
			return within(path, field.Path) && name != keyName
		}, secret)
		return &c
	}
	c.Fields = make([]*Field, len(field.Fields))
	for i, child := range field.Fields {
		c.Fields[i] = f.trim(child, false, stub, secret)
	}
	c.Entries = make([]*Field, len(field.Entries))
	for i, child := range field.Entries {
		c.Entries[i] = f.trim(child, false, stub, secret)
	}
	if field.Choice != nil {
		c.Choice = f.trim(field.Choice, true, stub, secret)
	}
	return &c
}

// controls returns the form state controls that keep reports, minus the
// mode and step (which the form renders itself) and writeOnly values
// (which are never sent back to the browser).
func (f *Form) controls(keep func(name, path string) bool, secret []string) []Control {
	var out []Control
	for _, name := range f.state.sortedNames() {
		if name == metaName(metaMode, "") || name == metaName(metaStep, "") {
			continue
		}
		_, path, ok := parseName(name)
		if !ok || !keep(name, path) || slices.ContainsFunc(secret, func(s string) bool { return within(path, s) }) {
			continue
		}
		for _, value := range f.state[name] {
			out = append(out, Control{Name: name, Value: value})
		}
	}
	return out
}

func isGroup(f *Field) bool {
	if f.Collapsed {
		return false
	}
	switch f.Kind {
	case KindObject, KindMap, KindArray, KindChoice:
		return true
	}
	return false
}

func summary(f *Field) string {
	switch {
	case f.Choice != nil:
		return f.Choice.Label
	case f.Kind == KindArray:
		return strconv.Itoa(len(f.Fields)) + " " + plural(len(f.Fields), "item", "items")
	case f.Kind == KindMap:
		return strconv.Itoa(len(f.Entries)) + " " + plural(len(f.Entries), "property", "properties")
	}
	return ""
}

type groupRef struct {
	field  *Field
	parent int // index of the enclosing group, or -1
}

// groupsOf lists the groups in document order: the wizard's steps.
func groupsOf(root *Field) []groupRef {
	var out []groupRef
	var walk func(f *Field, parent int)
	walk = func(f *Field, parent int) {
		if isGroup(f) {
			out = append(out, groupRef{field: f, parent: parent})
			parent = len(out) - 1
		}
		content := f
		if f.Choice != nil {
			content = f.Choice
		}
		for _, c := range content.Fields {
			walk(c, parent)
		}
		for _, c := range content.Entries {
			walk(c, parent)
		}
	}
	walk(root, -1)
	return out
}

func wizardFor(groups []groupRef, step int) *Wizard {
	crumb := func(i int) *Crumb {
		if i < 0 || i >= len(groups) {
			return nil
		}
		g := groups[i].field
		return &Crumb{Label: firstNonEmpty(g.Label, g.Pointer, "Value"), Path: g.Path, Current: i == step}
	}
	w := &Wizard{
		Step: groups[step].field.Path,
		Back: crumb(step - 1),
		Up:   crumb(groups[step].parent),
		Next: crumb(step + 1),
	}
	for i := step; i >= 0; i = groups[i].parent {
		w.Crumbs = append([]Crumb{*crumb(i)}, w.Crumbs...)
	}
	for i, g := range groups {
		if i != step && ownsErrors(g.field, true) {
			w.Elsewhere = append(w.Elsewhere, *crumb(i))
		}
	}
	return w
}

// ownsErrors reports errors on a group or on the fields its own step
// shows, not counting the groups inside it, which are steps of their own.
func ownsErrors(f *Field, top bool) bool {
	if !top && isGroup(f) {
		return false
	}
	if len(f.Errors) > 0 || (f.Key != nil && len(f.Key.Errors) > 0) {
		return true
	}
	if f.Choice != nil && ownsErrors(f.Choice, true) {
		return true
	}
	for _, c := range append(slices.Clip(f.Fields), f.Entries...) {
		if ownsErrors(c, false) {
			return true
		}
	}
	return false
}

// invalidWithin reports errors on the field or any field inside it.
func (f *Field) invalidWithin() bool {
	if f.Invalid() || (f.Key != nil && len(f.Key.Errors) > 0) {
		return true
	}
	for _, c := range append(slices.Clip(f.Fields), f.Entries...) {
		if c.invalidWithin() {
			return true
		}
	}
	return f.Choice != nil && f.Choice.invalidWithin()
}

func (f *Form) outline(field *Field, w *Wizard) []OutlineItem {
	if field.Choice != nil {
		return f.outline(field.Choice, w)
	}
	var items []OutlineItem
	for _, c := range append(slices.Clip(field.Fields), field.Entries...) {
		if !isGroup(c) {
			continue
		}
		item := OutlineItem{
			Label:   firstNonEmpty(c.Label, c.Pointer),
			Href:    "#" + c.GroupID(),
			Path:    c.Path,
			Invalid: c.invalidWithin(),
			Folded:  f.folded(c.Path),
			Wizard:  w != nil,
			Current: w != nil && w.Step == c.Path,
		}
		if c.Choice != nil {
			item.Note = c.Choice.Label
		}
		if !item.Folded {
			item.Items = f.outline(c, w)
		}
		items = append(items, item)
	}
	return items
}
