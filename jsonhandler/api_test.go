package jsonhandler

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// A document whose definitions refer to each other, with its own $id.
const definitions = `{
	"$id": "https://example.com/catalog.json",
	"type": "object",
	"properties": {"items": {"type": "array", "items": {"$ref": "#/$defs/item"}}},
	"$defs": {
		"item": {"title": "Item", "type": "object", "required": ["name", "price"], "properties": {"name": {"type": "string"}, "price": {"$ref": "#/$defs/money"}}},
		"money": {"type": "number", "minimum": 0}
	}
}`

// TestSaveAfterSubmit is the flow an app uses without OnSubmit: start from
// the stored value, save after a valid submission, and show the form again
// when saving fails.
func TestSaveAfterSubmit(t *testing.T) {
	ctx := context.Background()
	base, err := NewAt(ctx, []byte(definitions), "/$defs/item")
	if err != nil {
		t.Fatal(err)
	}
	h := base.WithValue(jsontext.Value(`{"name":"pen","price":2}`))
	if base.Value != nil {
		t.Errorf("WithValue changed the handler it was called on")
	}
	f, err := h.Form(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Root.Fields[0].Value; got != "pen" {
		t.Errorf("form starts with name %q, want pen", got)
	}

	f, err = h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"3"}})
	if err != nil || !f.Valid {
		t.Fatalf("submission valid = %v, err %v", f != nil && f.Valid, err)
	}
	f.Reject(errors.New("That name is taken."))
	if f.Valid || f.Value != nil || f.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("after Reject: valid %v, value %s, status %d", f.Valid, f.Value, f.StatusCode())
	}
	if !slices.Equal(f.Errors, []string{"That name is taken."}) {
		t.Errorf("form errors = %q", f.Errors)
	}
	if got := f.Root.Fields[1].Value; got != "3" {
		t.Errorf("the rejected form shows price %q, want what was submitted", got)
	}
}

func TestFormHTML(t *testing.T) {
	ctx := context.Background()
	h, err := NewAt(ctx, []byte(definitions), "/$defs/item")
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, f *Form) string {
		t.Helper()
		tmpl, err := Templates()
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		if err := tmpl.ExecuteTemplate(&b, name, f); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	f, err := h.Form(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.WithActions("/items/form", "/items/form/validate")
	got, err := f.HTML()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render("jsonhandler-form", f) {
		t.Errorf("HTML() differs from the jsonhandler-form template")
	}

	v, err := h.Validate(ctx, url.Values{"$/name": {"pen"}, "$/price": {"-1"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = v.ValidationHTML()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render("jsonhandler-validation", v) || !strings.Contains(string(got), "less than minimum 0") {
		t.Errorf("ValidationHTML() = %s", got)
	}

	ok, err := h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"2"}})
	if err != nil || !ok.Valid {
		t.Fatalf("valid submission: %v", err)
	}
	got, err = ok.HTML()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render("jsonhandler-result", ok) {
		t.Errorf("HTML() of a valid submission is not the result partial")
	}
}

func TestHandler_modes(t *testing.T) {
	ctx := context.Background()
	const schema = `{"type":"object","properties":{"a":{"type":"string"}}}`
	page := func(h *Handler, form url.Values) (*Form, string) {
		t.Helper()
		f, err := h.SubmitForm(ctx, form)
		if err != nil {
			t.Fatal(err)
		}
		html, err := f.WithActions("/f", "").HTML()
		if err != nil {
			t.Fatal(err)
		}
		return f, string(html)
	}
	const jsonButton, wizardButton = `value="json"`, `value="step"`
	post := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Add(kv[i], kv[i+1])
		}
		return v
	}

	t.Run("both are offered by default", func(t *testing.T) {
		_, html := page(newHandler(t, schema), post("@action", "refresh"))
		if !strings.Contains(html, jsonButton) || !strings.Contains(html, wizardButton) {
			t.Errorf("the default toolbar lacks a mode button")
		}
	})

	t.Run("NoJSON", func(t *testing.T) {
		h := newHandler(t, schema)
		h.NoJSON = true
		for name, form := range map[string]url.Values{
			"asked for":       post("@action", ActionJSON),
			"already in it":   post("@mode", ModeJSON, "$", `{"a":"x"}`, "@action", "refresh"),
			"from the wizard": post("@mode", ModeWizard, "@step", "/a", "@action", "refresh"),
		} {
			f, html := page(h, form)
			if f.Mode == ModeJSON || strings.Contains(html, jsonButton) || strings.Contains(html, `name="$"`) {
				t.Errorf("%s: mode %q, and the page offers JSON:\n%s", name, f.Mode, html)
			}
		}
		if f, _ := page(h, post("@action", ActionWizard)); f.Mode != ModeWizard {
			t.Errorf("turning off JSON also turned off the wizard: mode %q", f.Mode)
		}
	})

	t.Run("NoWizard", func(t *testing.T) {
		h := newHandler(t, schema)
		h.NoWizard = true
		for name, form := range map[string]url.Values{
			"asked for":     post("@action", ActionWizard),
			"next":          post("@action", "next/a"),
			"already in it": post("@mode", ModeWizard, "@step", "/a", "@action", "refresh"),
		} {
			f, html := page(h, form)
			if f.Mode == ModeWizard || strings.Contains(html, wizardButton) {
				t.Errorf("%s: mode %q, and the page offers the wizard", name, f.Mode)
			}
		}
		if f, _ := page(h, post("@action", ActionJSON)); f.Mode != ModeJSON {
			t.Errorf("turning off the wizard also turned off JSON: mode %q", f.Mode)
		}
	})
}

// A handler is cheap to make: an app that builds one per request, from a
// schema that comes from data, must not pay for the meta-schemas unless
// the schema refers to them. (Declaring $schema does refer to one: the
// resolver loads it to learn the vocabularies, a cost of well under a
// millisecond that an app avoids by keeping the handler.)
func TestNew_cost(t *testing.T) {
	ctx := context.Background()
	for name, schema := range map[string]string{
		"plain":            `{"type":"string"}`,
		"with definitions": definitions,
	} {
		allocs := testing.AllocsPerRun(5, func() {
			if _, err := New(ctx, []byte(schema)); err != nil {
				t.Fatal(err)
			}
		})
		if allocs > 2000 {
			t.Errorf("New(%s) allocates %.0f times, want at most 2000", name, allocs)
		}
	}
	// A schema that does refer to a meta-schema still gets it.
	if _, err := New(ctx, []byte(`{"$ref":"https://json-schema.org/draft/2020-12/meta/validation"}`)); err != nil {
		t.Errorf("a reference to a vocabulary meta-schema: %v", err)
	}
}

// An optional radio group has a way to say nothing, and it says so: an
// unlabelled radio that starts out checked looks like the first option
// being pre-selected.
func TestRadio_blankIsLabeled(t *testing.T) {
	h := newHandler(t, `{"type":"object","required":["must"],"properties":{
		"pick":   {"enum":["a","b"],"x-form":{"widget":"radio"}},
		"custom": {"enum":["a","b"],"x-form":{"widget":"radio","placeholder":"Skip"}},
		"must":   {"enum":["a","b"],"x-form":{"widget":"radio"}},
		"menu":   {"enum":["a","b"]}}}`)
	f, err := h.Form(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	labels := func(name string) (labels []string, blankChecked bool) {
		for _, field := range f.Root.Fields {
			if field.Pointer != "/"+name {
				continue
			}
			for _, o := range field.Options {
				labels = append(labels, o.Label)
				blankChecked = blankChecked || (o.Value == "" && o.Selected)
			}
		}
		return labels, blankChecked
	}
	for name, want := range map[string][]string{
		"pick":   {"No answer", "a", "b"},
		"custom": {"Skip", "a", "b"},
		"must":   {"a", "b"},
		"menu":   {"", "a", "b"},
	} {
		got, _ := labels(name)
		if !slices.Equal(got, want) {
			t.Errorf("%s option labels = %q, want %q", name, got, want)
		}
	}
	if _, checked := labels("pick"); !checked {
		t.Errorf("an unanswered optional radio group does not start on its blank option")
	}
}

// The value being edited is always there, so its label is not marked as
// required; the properties inside it are, where they are.
func TestRoot_notMarkedRequired(t *testing.T) {
	h := newHandler(t, `{"title":"Survey","type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`)
	f, err := h.Form(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	html, err := f.HTML()
	if err != nil {
		t.Fatal(err)
	}
	legend, _, _ := strings.Cut(string(html), "</legend>")
	_, legend, _ = strings.Cut(legend, `<legend class="jsonform-label">`)
	if !strings.Contains(legend, "Survey") || strings.Contains(legend, "jsonform-required") {
		t.Errorf("the root's legend is %q, want the title with no required mark", legend)
	}
	if n := strings.Count(string(html), `class="jsonform-required"`); n != 1 {
		t.Errorf("%d required marks in the form, want one (for a)", n)
	}
}

// A submission with several problems shows them all at once, whichever
// kind they are: a field left blank or not a number is found while
// reading the form, the rest by validating the schema. Showing only the
// first kind makes people fix a form one round at a time.
func TestSubmitForm_allErrorsAtOnce(t *testing.T) {
	h := newHandler(t, `{"type":"object","required":["name","tags","size"],"properties":{
		"name": {"type":"string"},
		"size": {"type":"number","minimum":1},
		"tags": {"type":"array","minItems":1,"uniqueItems":true,"items":{"enum":["a","b"]}},
		"code": {"type":"string","pattern":"^[0-9]+$"},
		"age":  {"type":"integer","minimum":0}}}`)
	for name, tc := range map[string]struct {
		form url.Values
		want map[string][]string
	}{
		"blank and a short list": {
			url.Values{"@present": {"1"}, "$/name": {"n"}, "$/size": {""}, "$/code": {"x1"}},
			map[string][]string{"/size": {"required"}, "/tags": {"array has 0 items, minItems 1"}, "/code": {`string does not match pattern "^[0-9]+$"`}},
		},
		"not a number and a bad pattern": {
			url.Values{"@present": {"1"}, "$/name": {"n"}, "$/size": {"abc"}, "$/tags": {"a"}, "$/code": {"x1"}},
			map[string][]string{"/size": {messageNumber}, "/code": {`string does not match pattern "^[0-9]+$"`}},
		},
		"only reading problems": {
			url.Values{"@present": {"1"}, "$/name": {"n"}, "$/size": {""}, "$/tags": {"a"}, "$/code": {"1"}},
			map[string][]string{"/size": {"required"}},
		},
	} {
		f, err := h.SubmitForm(context.Background(), tc.form)
		if err != nil {
			t.Fatal(err)
		}
		got := fieldErrors(f.Root)
		if !maps.EqualFunc(got, tc.want, slices.Equal) {
			t.Errorf("%s: field errors = %q, want %q", name, got, tc.want)
		}
		if len(f.Errors) != 0 {
			t.Errorf("%s: form errors = %q, want none", name, f.Errors)
		}
		if f.Valid || f.StatusCode() != http.StatusUnprocessableEntity {
			t.Errorf("%s: valid %v, status %d", name, f.Valid, f.StatusCode())
		}
	}
}

// Nothing is wrong with a group nobody has reached yet: live validation and
// the wizard leave an untouched list or checkbox group alone, as they do a
// blank required field, until a submission asks for it.
func TestLive_untouchedGroupsStaySilent(t *testing.T) {
	ctx := context.Background()
	h := newHandler(t, `{"type":"object","required":["size","tags","steps"],"properties":{
		"size":  {"type":"number","minimum":1},
		"tags":  {"type":"array","minItems":1,"uniqueItems":true,"items":{"enum":["a","b"]}},
		"steps": {"type":"array","minItems":1,"items":{"type":"string"}}}}`)

	v, err := h.Validate(ctx, url.Values{"@present": {"1"}, "$/size": {"3"}, "@len/steps": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldErrors(v.Root); len(got) != 0 {
		t.Errorf("live validation of an untouched form shows %q", got)
	}
	// What was typed is still checked.
	v, err = h.Validate(ctx, url.Values{"@present": {"1"}, "$/size": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldErrors(v.Root); !maps.EqualFunc(got, map[string][]string{"/size": {"less than minimum 1"}}, slices.Equal) {
		t.Errorf("live validation of a typed value shows %q", got)
	}

	w, err := h.SubmitForm(ctx, url.Values{ActionName: {ActionWizard}})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Wizard().Elsewhere; len(got) != 0 {
		t.Errorf("entering the wizard flags steps nobody reached: %+v", got)
	}

	// A submission still asks for them.
	f, err := h.SubmitForm(context.Background(), url.Values{"@present": {"1"}, "$/size": {"3"}, "@len/steps": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"/tags": {"array has 0 items, minItems 1"}, "/steps": {"array has 0 items, minItems 1"}}
	if got := fieldErrors(f.Root); !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("a submission shows %q, want %q", got, want)
	}
}

func TestNewAt(t *testing.T) {
	ctx := context.Background()
	h, err := NewAt(ctx, []byte(definitions), "/$defs/item")
	if err != nil {
		t.Fatal(err)
	}
	f, err := h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldErrors(f.Root)["/price"]; len(got) != 1 || got[0] != "less than minimum 0" {
		t.Errorf("errors on /price = %q; the reference to money did not resolve", got)
	}
	f, err = h.SubmitForm(ctx, url.Values{"$/name": {"pen"}, "$/price": {"2.5"}})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Valid || string(f.Value) != `{"name":"pen","price":2.5}` {
		t.Errorf("valid submission = %v %s", f.Valid, f.Value)
	}
	if _, err := NewAt(ctx, []byte(definitions), "/$defs/nope"); err == nil {
		t.Errorf("NewAt with a pointer to nothing succeeded")
	}
}
