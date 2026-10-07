package jsonhandler

import (
	"slices"
	"strings"
)

// The wizard walks through the value one field at a time. Its steps are
// the fields in document order:
//
//   - a scalar field (text, number, checkbox, enum, JSON) is a step;
//   - an array, map or checkbox group is one step holding all of it;
//   - a choice is a step with its variant picker, and when the chosen
//     variant is an object its properties are the steps after it;
//   - an optional value that starts out left out is a step offering to
//     add it;
//   - an object's undeclared properties, and a wide object's picker of
//     properties to add, are a step each.
//
// Objects are not steps of their own; they are where a step is, shown as
// its breadcrumb.

// Wizard is what the wizard shows around the current step.
type Wizard struct {
	// Step is the current step's key: the form path of its field, or of
	// an object followed by "/~entries" or "/~pick".
	Step string
	// Label names the current step, Index counts it from 1, and Count is
	// how many steps there are.
	Label        string
	Index, Count int
	// Crumbs are the objects the step is in, outermost first; each goes
	// to the object's first step.
	Crumbs []Crumb
	// Back and Next are the steps before and after; nil where there is
	// none.
	Back, Next *Crumb
	// Elsewhere lists the other steps with errors.
	Elsewhere []Crumb
}

// Crumb names a step, or an object to go to the first step of.
type Crumb struct {
	Label, Path string
}

// Action is the value of the edit button that goes to the step.
func (c Crumb) Action() string { return ActionWizard + c.Path }

// NextAction is the value of the Next button. Unlike going to a step
// directly, Next refuses to leave a step that is required and empty or
// has errors.
func (w *Wizard) NextAction() string {
	if w.Next == nil {
		return ""
	}
	return "next" + w.Next.Path
}

type stepKind int

const (
	stepField   stepKind = iota // a field, rendered whole
	stepChoice                  // a choice whose chosen object is walked after it
	stepEntries                 // an object's undeclared properties
	stepPick                    // a wide object's picker of properties to add
)

type step struct {
	key    string
	label  string
	kind   stepKind
	field  *Field
	groups []*Field // the objects (or choices) the step is in, outermost first
}

func stepsOf(root *Field) []step {
	var out []step
	var walk func(f *Field, groups []*Field)
	var object func(obj, crumb *Field, groups []*Field)
	walk = func(f *Field, groups []*Field) {
		switch {
		case f.Collapsed:
			out = append(out, step{key: f.Path, label: f.Label, field: f, groups: groups})
		case f.Kind == KindConst, f.Kind == KindNull:
			// Nothing to fill in.
		case f.Kind == KindObject:
			object(f, f, groups)
		case f.Kind == KindChoice && f.Choice != nil && f.Choice.Kind == KindObject:
			out = append(out, step{key: f.Path, label: f.Label, kind: stepChoice, field: f, groups: groups})
			object(f.Choice, f, groups)
		default:
			out = append(out, step{key: f.Path, label: f.Label, field: f, groups: groups})
		}
	}
	object = func(obj, crumb *Field, groups []*Field) {
		inner := append(slices.Clip(groups), crumb)
		for _, c := range obj.Fields {
			walk(c, inner)
		}
		name := " "
		if crumb.Label != "" {
			name = " " + crumb.Label + " "
		}
		if obj.LenName != "" {
			out = append(out, step{key: obj.Path + "/~entries", label: "Other" + name + "properties", kind: stepEntries, field: obj, groups: inner})
		}
		if len(obj.Absent) > 0 {
			out = append(out, step{key: obj.Path + "/~pick", label: "More" + name + "properties", kind: stepPick, field: obj, groups: inner})
		}
	}
	walk(root, nil)
	return out
}

// render returns the field a step shows.
func (s step) render() *Field {
	c := *s.field
	switch s.kind {
	case stepChoice:
		c.Choice = nil // its properties are the steps that follow
	case stepEntries:
		c.Fields, c.Absent, c.PickName = nil, nil, ""
	case stepPick:
		c.Fields, c.Entries, c.LenName = nil, nil, ""
	}
	return &c
}

// wizard returns the current step's field, the controls it does not
// render, and the navigation around it.
func (f *Form) wizard(secret []string) (*Field, []Control, *Wizard) {
	steps := stepsOf(f.Root)
	if len(steps) == 0 {
		// Nothing to fill in: show the value as it is.
		return f.Root, nil, &Wizard{Label: firstNonEmpty(f.Root.Label, "Value")}
	}
	i := findStep(steps, f.step)
	st := steps[i]
	shown := st.render()
	rendered := renderedNames(shown)
	hidden := f.controls(func(name, _ string) bool { return !rendered[name] }, secret)
	crumb := func(j int) *Crumb {
		if j < 0 || j >= len(steps) {
			return nil
		}
		return &Crumb{Label: firstNonEmpty(steps[j].label, steps[j].field.Pointer, "Value"), Path: steps[j].key}
	}
	w := &Wizard{
		Step:  st.key,
		Label: crumb(i).Label,
		Index: i + 1,
		Count: len(steps),
		Back:  crumb(i - 1),
		Next:  crumb(i + 1),
	}
	for _, g := range st.groups {
		w.Crumbs = append(w.Crumbs, Crumb{Label: firstNonEmpty(g.Label, "Value"), Path: g.Path})
	}
	for j, other := range steps {
		if j != i && len(problems(other.render(), false)) > 0 {
			w.Elsewhere = append(w.Elsewhere, *crumb(j))
		}
	}
	return shown, hidden, w
}

// findStep returns the step with the key, else the first step inside the
// key (an object's path), else the first step.
func findStep(steps []step, key string) int {
	if i := slices.IndexFunc(steps, func(s step) bool { return s.key == key }); i >= 0 {
		return i
	}
	if i := slices.IndexFunc(steps, func(s step) bool { return within(s.key, key) }); i >= 0 {
		return i
	}
	return 0
}

// renderedNames lists the controls the templates render for a field.
func renderedNames(f *Field) map[string]bool {
	names := map[string]bool{}
	var walk func(*Field)
	walk = func(f *Field) {
		if f.Collapsed {
			return
		}
		switch f.Kind {
		case KindText, KindTextarea, KindNumber, KindJSON, KindCheckbox, KindSelect, KindRadio, KindCheckboxes:
			names[f.Name] = true
		case KindChoice:
			names[f.VariantName] = true
		}
		for _, name := range []string{f.LenName, f.PresentName, f.PickName} {
			if name != "" {
				names[name] = true
			}
		}
		if f.Key != nil {
			names[f.Key.Name] = true
		}
		for _, c := range f.Fields {
			walk(c)
		}
		for _, c := range f.Entries {
			walk(c)
		}
		if f.Choice != nil {
			walk(f.Choice)
		}
	}
	walk(f)
	return names
}

// problems lists what keeps a step from being done: the errors on its
// fields and, when strict, the required fields left empty.
func problems(f *Field, strict bool) []fieldError {
	var out []fieldError
	var walk func(*Field)
	walk = func(f *Field) {
		if f.Collapsed {
			return
		}
		if strict && f.HTMLRequired && blank(f) {
			// "Required" says more than what an empty value breaks.
			out = append(out, fieldError{path: f.Path, message: messageRequired})
		} else {
			for _, m := range f.Errors {
				out = append(out, fieldError{path: f.Path, message: m})
			}
		}
		if f.Key != nil {
			for _, m := range f.Key.Errors {
				out = append(out, fieldError{path: f.Path, key: true, message: m})
			}
		}
		for _, c := range f.Fields {
			walk(c)
		}
		for _, c := range f.Entries {
			walk(c)
		}
		if f.Choice != nil {
			walk(f.Choice)
		}
	}
	walk(f)
	return out
}

// quiet drops the errors on fields left blank.
func (f *Form) quiet() {
	for _, field := range f.fields(blank) {
		field.Errors = nil
	}
}

// blank reports a control left empty.
func blank(f *Field) bool {
	switch f.Kind {
	case KindText, KindTextarea, KindNumber, KindJSON, KindSelect, KindRadio:
		return strings.TrimSpace(f.submitted()) == ""
	}
	return false
}
