package jsonhandler

import (
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

const personSchema = `{"title":"Person","type":"object","required":["name","pet"],"properties":{
	"name":{"type":"string","minLength":2},
	"nick":{"type":"string"},
	"age":{"type":"integer","minimum":0},
	"address":{"type":"object","required":["street"],"properties":{"street":{"type":"string"},"zip":{"type":"string"}}},
	"tags":{"type":"array","items":{"type":"string"}},
	"pet":{"oneOf":[
		{"title":"Dog","type":"object","required":["kind","name"],"properties":{"kind":{"const":"dog"},"name":{"type":"string"}}},
		{"title":"Cat","type":"string"}]},
	"secret":{"type":"string","writeOnly":true}
}}`

// stepOf reads the progress line: the step's number, count and label.
func (p *page) stepOf() (int, int, string) {
	p.t.Helper()
	var i, n int
	var label string
	if p.doc.QuerySelector(".jsonform-progress") == nil {
		return 0, 0, ""
	}
	fmt.Sscan(p.texts(".jsonform-progress-index")[0], &i)
	fmt.Sscan(p.texts(".jsonform-progress-count")[0], &n)
	label = p.texts(".jsonform-progress-label")[0]
	return i, n, label
}

// visible lists the names of the controls a person can see and use.
func (p *page) visible() []string {
	var out []string
	for el := range p.form().QuerySelectorSequence("input, select, textarea") {
		if el.GetAttribute("type") == "hidden" || !el.HasAttribute("name") {
			continue
		}
		if name := el.GetAttribute("name"); !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func TestWizard_walksFieldByField(t *testing.T) {
	h := newHandler(t, personSchema)
	p := open(t, h).press(ActionWizard)
	if got := p.form().GetAttribute("data-mode"); got != ModeWizard {
		t.Fatalf("data-mode = %q", got)
	}
	type want struct {
		label   string
		visible []string
		crumbs  []string
		fill    map[string]string
	}
	for i, w := range []want{
		{"name", []string{"$/name"}, []string{"Person"}, map[string]string{"$/name": "Ada"}},
		{"nick", []string{"$/nick"}, []string{"Person"}, nil},
		{"age", []string{"$/age"}, []string{"Person"}, map[string]string{"$/age": "36"}},
		{"street", []string{"$/address/street"}, []string{"Person", "address"}, nil},
		{"zip", []string{"$/address/zip"}, []string{"Person", "address"}, nil},
		{"tags", nil, []string{"Person"}, nil},
		{"pet", []string{"@variant/pet"}, []string{"Person"}, nil},
		{"name", []string{"$/pet/name"}, []string{"Person", "pet"}, map[string]string{"$/pet/name": "Rex"}},
		{"secret", []string{"$/secret"}, []string{"Person"}, map[string]string{"$/secret": "s3cret"}},
	} {
		index, count, label := p.stepOf()
		if index != i+1 || count != 9 || label != w.label {
			t.Fatalf("step %d: got step %d of %d %q, want %q", i+1, index, count, label, w.label)
		}
		if got := p.visible(); !slices.Equal(got, w.visible) {
			t.Errorf("step %d (%s) shows %q, want %q", i+1, label, got, w.visible)
		}
		if got := p.texts(".jsonform-crumbs button"); !slices.Equal(got, w.crumbs) {
			t.Errorf("step %d (%s) crumbs %q, want %q", i+1, label, got, w.crumbs)
		}
		if (i == 0) != !p.has(".jsonform-back") {
			t.Errorf("step %d: back button present = %v", i+1, p.has(".jsonform-back"))
		}
		for name, value := range w.fill {
			p.set(name, value)
		}
		if i == 8 {
			if p.has(".jsonform-next") {
				t.Errorf("the last step has a next button")
			}
			break
		}
		next := p.doc.QuerySelector(".jsonform-next")
		if next == nil {
			t.Fatalf("step %d has no next button", i+1)
		}
		p = p.press(next.GetAttribute("value"))
	}
	res := p.submit()
	if !res.has(".jsonform-result") {
		t.Fatalf("submission failed: %s", res.form().OuterHTML())
	}
	got := jsontext.Value(res.doc.QuerySelector(".jsonform-result code").TextContent())
	assertJSONEqual(t, got, `{"name":"Ada","age":36,"pet":{"kind":"dog","name":"Rex"},"secret":"********"}`)
}

func TestWizard_requiredFieldsCannotBeSkipped(t *testing.T) {
	h := newHandler(t, personSchema)
	p := open(t, h).press(ActionWizard)

	next := p.doc.QuerySelector(".jsonform-next")
	if next.HasAttribute("formnovalidate") {
		t.Errorf("next skips the browser's check of required fields")
	}
	if input := p.doc.QuerySelector(`input[name="$/name"]`); !input.HasAttribute("required") {
		t.Errorf("a required field is not required in the page")
	}
	if b := p.doc.QuerySelector(`button[hidden]`); b.GetAttribute("value") != "next/nick" {
		t.Errorf("enter does not go to the next step: %s", b.OuterHTML())
	}

	if got := p.texts(".jsonform-errors li"); len(got) > 0 {
		t.Errorf("a fresh wizard shows errors on blank fields: %q", got)
	}

	stay := p.press("next/nick")
	if stay.code != http.StatusUnprocessableEntity {
		t.Errorf("next past an empty required field: status %d", stay.code)
	}
	if _, _, label := stay.stepOf(); label != "name" {
		t.Errorf("next past an empty required field went to %q", label)
	}
	if got := stay.texts(`[data-pointer="/name"] .jsonform-errors li`); !slices.Equal(got, []string{messageRequired}) {
		t.Errorf("errors = %q", got)
	}
	if stay.has(".jsonform-elsewhere") {
		t.Errorf("empty later steps are reported while still on the first")
	}

	short := p.set("$/name", "A").press("next/nick")
	if _, _, label := short.stepOf(); label != "name" || len(short.texts(`[data-pointer="/name"] .jsonform-errors li`)) != 1 {
		t.Errorf("next past an invalid field went to %q", label)
	}

	// Optional fields can be left empty, including required ones inside an
	// optional object, which is left out when nothing in it is filled in.
	p = p.set("$/name", "Ada").press("next/nick")
	for _, step := range []string{"next/age", "next/address/street", "next/address/zip", "next/tags"} {
		before := p
		p = p.press(step)
		if _, _, label := p.stepOf(); label == "" || label == func() string { _, _, l := before.stepOf(); return l }() {
			t.Fatalf("%s did not move on from %s", step, label)
		}
	}
	if input := open(t, h).jump("/address/street").doc.QuerySelector(`input[name="$/address/street"]`); input.HasAttribute("required") {
		t.Errorf("a required field in an optional object is required in the page")
	}

	// Back and jumping are never held back.
	if b := p.doc.QuerySelector(".jsonform-back"); !b.HasAttribute("formnovalidate") {
		t.Errorf("back checks required fields")
	}
	jump := open(t, h).press(ActionWizard).jump("/pet")
	if _, _, label := jump.stepOf(); label != "pet" {
		t.Errorf("jumping past an empty required field went to %q", label)
	}
}

func TestWizard_choice(t *testing.T) {
	h := newHandler(t, personSchema)
	p := open(t, h).jump("/pet")
	if _, n, _ := p.stepOf(); n != 9 {
		t.Errorf("with the dog variant there are %d steps", n)
	}
	// Choosing the cat, a string, puts its input on the choice's own step
	// and drops the dog's steps.
	p.form().QuerySelector(`select[name="@variant/pet"] option[value="0"]`).RemoveAttribute("selected")
	p.form().QuerySelector(`select[name="@variant/pet"] option[value="1"]`).SetAttribute("selected", "")
	p = p.press("refresh")
	i, n, label := p.stepOf()
	if label != "pet" || n != 8 {
		t.Errorf("after choosing the cat: step %d of %d %q", i, n, label)
	}
	if got := p.visible(); !slices.Equal(got, []string{"@variant/pet", "$/pet"}) {
		t.Errorf("cat step shows %q", got)
	}
	if next := p.doc.QuerySelector(".jsonform-next"); next.GetAttribute("value") != "next/secret" {
		t.Errorf("next after the cat = %s", next.GetAttribute("value"))
	}
}

func TestWizard_objectSteps(t *testing.T) {
	t.Run("undeclared properties", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"integer"}}`)
		h.Value = jsontext.Value(`{"a":"x","b":1}`)
		p := open(t, h).press(ActionWizard).press("next/~entries")
		if _, n, label := p.stepOf(); n != 2 || label != "Other properties" {
			t.Errorf("entries step = %q of %d", label, n)
		}
		if got := p.visible(); !slices.Equal(got, []string{"@key/~k0", "$/~k0"}) {
			t.Errorf("entries step shows %q", got)
		}
		if p.has(`input[name="$/a"]:not([type="hidden"])`) {
			t.Errorf("entries step shows declared properties")
		}
	})

	t.Run("picker of a wide object", func(t *testing.T) {
		var props []string
		for i := range foldThreshold + 1 {
			props = append(props, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
		}
		h := newHandler(t, `{"title":"Wide","type":"object","properties":{`+strings.Join(props, ",")+`}}`)
		h.Value = jsontext.Value(`{"p0":"x"}`)
		p := open(t, h).press(ActionWizard)
		if _, n, label := p.stepOf(); n != 2 || label != "p0" {
			t.Errorf("first step = %q of %d", label, n)
		}
		p = p.press("next/~pick")
		if !p.has("#jsonform_root_pick") || p.has(`input[name="$/p0"]:not([type="hidden"])`) {
			t.Errorf("picker step = %s", p.form().OuterHTML())
		}
		p.form().QuerySelector(`#jsonform_root_pick option[value="p3"]`).SetAttribute("selected", "")
		p = p.press("pick")
		if _, n, _ := p.stepOf(); n != 3 {
			t.Errorf("after picking a property there are %d steps", n)
		}
	})

	t.Run("collapsed optional value", func(t *testing.T) {
		h := newHandler(t, `{"type":"object","properties":{"name":{"type":"string"},"next":{"$ref":"#"}}}`)
		p := open(t, h).jump("/next")
		if !p.has(`[data-pointer="/next"][data-collapsed] button[value="expand/next"]`) {
			t.Errorf("collapsed step = %s", p.form().OuterHTML())
		}
		p = p.press("expand/next")
		if _, n, _ := p.stepOf(); n != 3 {
			t.Errorf("after adding next there are %d steps", n)
		}
	})
}

func TestWizard_state(t *testing.T) {
	h := newHandler(t, personSchema)
	h.Outline = true
	h.Value = jsontext.Value(`{"name":"Ada","nick":"A","address":{"street":"Elm"},"tags":["x"],"pet":{"kind":"dog","name":"Rex"}}`)
	p := open(t, h).press(ActionWizard).set("$/name", "Grace").press("next/nick")
	if el := p.doc.QuerySelector(`input[type="hidden"][name="$/name"]`); el == nil || el.GetAttribute("value") != "Grace" {
		t.Errorf("the name typed on the first step is not carried")
	}
	p = p.jump("/secret").set("$/secret", "s3cret").jump("/name")
	if strings.Contains(p.form().OuterHTML(), "s3cret") {
		t.Errorf("a write-only value rides along in hidden inputs")
	}

	// A control the step renders is not carried a second time.
	tags := p.jump("/tags")
	if !tags.has(`input[name="$/tags/0"]:not([type="hidden"])`) || len(tags.texts(`input[name="@len/tags"]`)) != 1 {
		t.Errorf("tags step = %s", tags.form().OuterHTML())
	}

	// The outline marks the innermost group holding the step and goes to
	// a group's first field.
	p = p.jump("/address")
	if _, _, label := p.stepOf(); label != "street" {
		t.Errorf("the address goes to %q", label)
	}
	if got := p.texts(`.jsonform-outline li[aria-current="step"] > .jsonform-outline-label`); !slices.Equal(got, []string{"address"}) {
		t.Errorf("current outline group = %q", got)
	}
	if got := p.texts(`.jsonform-crumbs button`); !slices.Equal(got, []string{"Person", "address"}) {
		t.Errorf("crumbs = %q", got)
	}

	// Every value survives the trip, and other modes see all of it.
	res := p.submit()
	assertJSONEqual(t, jsontext.Value(res.doc.QuerySelector(".jsonform-result code").TextContent()), `{"name":"Grace","nick":"A","address":{"street":"Elm"},"tags":["x"],"pet":{"kind":"dog","name":"Rex"}}`)
	j := p.press(ActionJSON)
	assertJSONEqual(t, jsontext.Value(j.doc.QuerySelector(`textarea[name="$"]`).TextContent()), `{"name":"Grace","nick":"A","address":{"street":"Elm"},"tags":["x"],"pet":{"kind":"dog","name":"Rex"}}`)
	back := p.press(ActionFields)
	if back.form().HasAttribute("data-mode") || !back.has(`input[name="$/address/street"]:not([type="hidden"])`) {
		t.Errorf("edit as form did not show every field")
	}
}

func TestWizard_errorsElsewhere(t *testing.T) {
	h := newHandler(t, `{"type":"object","properties":{"n":{"type":"integer","maximum":3},"s":{"type":"string"},"z":{"type":"string"}}}`)
	h.Value = jsontext.Value(`{"n":9,"s":"x"}`)
	p := open(t, h).jump("/s").submit()
	if p.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", p.code)
	}
	if got := p.texts(".jsonform-elsewhere button"); !slices.Equal(got, []string{"n"}) {
		t.Errorf("errors elsewhere = %q", got)
	}
	// An error on another step does not hold back Next.
	p = open(t, h).jump("/s").press("next/z")
	if _, _, label := p.stepOf(); label != "z" {
		t.Errorf("went to %q", label)
	}
	p = p.jump("/n")
	if p.code != http.StatusOK {
		t.Errorf("going to a step with errors: status %d", p.code)
	}
	if got := p.texts(`[data-pointer="/n"] .jsonform-errors li`); len(got) != 1 {
		t.Errorf("going to the step shows its errors: %q", got)
	}
	if stay := p.press("next/s"); func() string { _, _, l := stay.stepOf(); return l }() != "n" {
		t.Errorf("next left a step with errors")
	}
}

func TestWizard_edges(t *testing.T) {
	t.Run("a scalar value is one step", func(t *testing.T) {
		p := open(t, newHandler(t, `{"type":"string"}`)).press(ActionWizard)
		if i, n, _ := p.stepOf(); i != 1 || n != 1 || p.has(".jsonform-next") || p.has(".jsonform-back") {
			t.Errorf("scalar wizard: step %d of %d", i, n)
		}
	})

	t.Run("nothing to fill in", func(t *testing.T) {
		p := open(t, newHandler(t, `{"type":"object","required":["k"],"properties":{"k":{"const":1}}}`)).press(ActionWizard)
		if p.has(".jsonform-progress") || !p.has(".jsonform-submit") {
			t.Errorf("const-only wizard = %s", p.form().OuterHTML())
		}
	})

	t.Run("an unknown step goes to the first", func(t *testing.T) {
		p := open(t, newHandler(t, personSchema)).post(url.Values{"@mode": {ModeWizard}, "@step": {"/nope"}, ActionName: {"refresh"}})
		if _, _, label := p.stepOf(); label != "name" {
			t.Errorf("went to %q", label)
		}
	})

	t.Run("live validation only targets rendered lists", func(t *testing.T) {
		h := newHandler(t, personSchema)
		p := open(t, h).jump("/address/street")
		req := formRequest(controls(p.form()))
		req.URL.Path = "/form/validate"
		v := fragment(t, serve(t, h, req))
		for el := range v.QuerySelectorSequence("ul") {
			if p.doc.QuerySelector("#"+el.ID()) == nil {
				t.Errorf("validation targets %s, which the page does not have", el.ID())
			}
		}
	})

	t.Run("the outline in the fields view opens the wizard at a group", func(t *testing.T) {
		h := newHandler(t, personSchema)
		h.Outline = true
		p := open(t, h)
		b := p.doc.QuerySelector(`.jsonform-outline button.jsonform-step[value="step/address"]`)
		if b == nil || b.GetAttribute("aria-label") != "Edit address with the wizard" {
			t.Fatalf("no wizard button for address")
		}
		if _, _, label := p.press("step/address").stepOf(); label != "street" {
			t.Errorf("went to %q", label)
		}
	})
}
