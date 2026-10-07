package jsonhandler

import (
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/typelate/dom/spec"
)

// page is a form as a browser holds it: the parsed response, whose
// controls a button press submits.
type page struct {
	t    *testing.T
	h    *Handler
	doc  spec.DocumentFragment
	code int
}

func open(t *testing.T, h *Handler) *page {
	t.Helper()
	res := getForm(t, h)
	return &page{t: t, h: h, doc: fragment(t, res), code: res.StatusCode}
}

func (p *page) form() spec.Element {
	p.t.Helper()
	f := p.doc.QuerySelector("form.jsonform")
	if f == nil {
		p.t.Fatalf("no form")
	}
	return f
}

// set changes an input's value, as typing would.
func (p *page) set(name, value string) *page {
	p.t.Helper()
	for el := range p.form().QuerySelectorSequence("input") {
		if el.GetAttribute("name") == name && el.GetAttribute("type") != "hidden" {
			el.SetAttribute("value", value)
			return p
		}
	}
	p.t.Fatalf("no input %s", name)
	return p
}

// press clicks the edit button with the value and follows the response.
func (p *page) press(value string) *page {
	p.t.Helper()
	var button spec.Element
	for el := range p.form().QuerySelectorSequence("button") {
		if el.GetAttribute("name") == ActionName && el.GetAttribute("value") == value {
			button = el
			break
		}
	}
	if button == nil {
		p.t.Fatalf("no button %q", value)
	}
	if button.HasAttribute("disabled") {
		p.t.Fatalf("button %q is disabled", value)
	}
	state := controls(p.form())
	state.Set(ActionName, value)
	return p.post(state)
}

// submit presses the submit button.
func (p *page) submit() *page {
	p.t.Helper()
	return p.post(controls(p.form()))
}

func (p *page) post(state url.Values) *page {
	p.t.Helper()
	res := postForm(p.t, p.h, state)
	return &page{t: p.t, h: p.h, doc: fragment(p.t, res), code: res.StatusCode}
}

func (p *page) has(selector string) bool { return p.doc.QuerySelector(selector) != nil }

func (p *page) texts(selector string) []string {
	var out []string
	for el := range p.doc.QuerySelectorSequence(selector) {
		out = append(out, strings.TrimSpace(el.TextContent()))
	}
	return out
}

const wizardSchema = `{"type":"object","required":["name"],"properties":{
	"name":{"type":"string"},
	"address":{"type":"object","properties":{"street":{"type":"string"},"geo":{"type":"object","properties":{"lat":{"type":"number"}}}}},
	"tags":{"type":"array","items":{"type":"string"}},
	"labels":{"type":"object","additionalProperties":{"type":"object","properties":{"color":{"type":"string"}}}}
}}`

func TestWizard(t *testing.T) {
	h := newHandler(t, wizardSchema)
	h.Outline = true
	h.Value = jsontext.Value(`{"name":"Ada","address":{"street":"Main","geo":{"lat":1}},"tags":["x"],"labels":{"bug":{"color":"red"}}}`)

	p := open(t, h).press(ActionWizard)
	if got := p.form().GetAttribute("data-mode"); got != ModeWizard {
		t.Fatalf("data-mode = %q", got)
	}
	if got := p.texts(".jsonform-crumbs li"); !slices.Equal(got, []string{"Value"}) && !slices.Equal(got, []string{""}) {
		t.Logf("root crumbs = %q", got)
	}
	// The root step edits name and shows the groups below as stubs.
	if !p.has(`input[name="$/name"]:not([type="hidden"])`) {
		t.Errorf("root step does not edit name")
	}
	for _, ptr := range []string{"/address", "/tags", "/labels"} {
		stub := p.doc.QuerySelector(`[data-pointer="` + ptr + `"][data-stub]`)
		if stub == nil || stub.QuerySelector(`button[value="step`+ptr+`"]`) == nil {
			t.Errorf("%s is not a stub with an edit button", ptr)
		}
	}
	if got := p.texts(`[data-pointer="/tags"] .jsonform-summary`); !slices.Equal(got, []string{"1 item"}) {
		t.Errorf("tags summary = %q", got)
	}
	if p.has(`input[name="$/address/street"]:not([type="hidden"])`) {
		t.Errorf("root step edits a field of a nested group")
	}
	if b := p.doc.QuerySelector(".jsonform-back"); b != nil {
		t.Errorf("first step has a back button")
	}

	// Change name, go to the next step, and change the street there.
	p = p.set("$/name", "Grace").press("step/address")
	if got := p.doc.QuerySelector(`input[name="@step"]`).GetAttribute("value"); got != "/address" {
		t.Errorf("@step = %q", got)
	}
	if got := p.texts(".jsonform-crumbs li"); len(got) != 2 || got[1] != "address" {
		t.Errorf("crumbs = %q", got)
	}
	if el := p.doc.QuerySelector(`.jsonform-crumbs li[aria-current="step"]`); el == nil || strings.TrimSpace(el.TextContent()) != "address" {
		t.Errorf("current crumb = %v", el)
	}
	if el := p.doc.QuerySelector(`input[type="hidden"][name="$/name"]`); el == nil || el.GetAttribute("value") != "Grace" {
		t.Errorf("name is not carried while editing the address: %v", el)
	}
	for _, want := range []string{".jsonform-back", ".jsonform-up-step", ".jsonform-next"} {
		if !p.has(want) {
			t.Errorf("no %s button", want)
		}
	}
	if el := p.doc.QuerySelector(`.jsonform-outline li[aria-current="step"] > button`); el == nil || el.GetAttribute("value") != "step/address" {
		t.Errorf("outline does not mark the step: %v", el)
	}
	p = p.set("$/address/street", "Elm")

	// Next goes into geo, then on to tags; Up comes back out.
	p = p.press("step/address/geo").set("$/address/geo/lat", "2.5")
	if next := p.doc.QuerySelector(".jsonform-next"); next == nil || next.GetAttribute("value") != "step/tags" {
		t.Errorf("next after geo = %v", next)
	}
	p = p.press("step/address")
	if el := p.doc.QuerySelector(`input[name="$/address/street"]`); el == nil || el.GetAttribute("value") != "Elm" {
		t.Errorf("street did not survive the trip into geo")
	}

	// Step into a map entry's value: the property name rides along.
	p = p.press("step/labels/~k0")
	if el := p.doc.QuerySelector(`input[type="hidden"][name="@key/labels/~k0"]`); el == nil || el.GetAttribute("value") != "bug" {
		t.Errorf("entry key is not carried: %v", el)
	}
	p = p.set("$/labels/~k0/color", "blue")

	res := p.submit()
	if !res.has(".jsonform-result") {
		t.Fatalf("submission failed: %s", res.form().OuterHTML())
	}
	got := res.doc.QuerySelector(".jsonform-result code").TextContent()
	assertJSONEqual(t, jsontext.Value(got), `{"name":"Grace","address":{"street":"Elm","geo":{"lat":2.5}},"tags":["x"],"labels":{"bug":{"color":"blue"}}}`)

	// Edit as form leaves the wizard with every value in place.
	back := p.press(ActionFields)
	if back.form().HasAttribute("data-mode") || back.has(`input[name="@step"]`) {
		t.Errorf("still in the wizard")
	}
	if el := back.doc.QuerySelector(`input[name="$/labels/~k0/color"]`); el == nil || el.GetAttribute("value") != "blue" {
		t.Errorf("value lost leaving the wizard")
	}

	// Edit as JSON from the wizard decodes everything, not just the step.
	j := p.press(ActionJSON)
	assertJSONEqual(t, jsontext.Value(j.doc.QuerySelector(`textarea[name="$"]`).TextContent()), `{"name":"Grace","address":{"street":"Elm","geo":{"lat":2.5}},"tags":["x"],"labels":{"bug":{"color":"blue"}}}`)
}

func TestWizard_errorsElsewhere(t *testing.T) {
	h := newHandler(t, `{"type":"object","properties":{"a":{"type":"object","properties":{"n":{"type":"integer","maximum":3}}},"b":{"type":"object","properties":{"s":{"type":"string"}}}}}`)
	h.Outline = true
	h.Value = jsontext.Value(`{"a":{"n":9},"b":{"s":"x"}}`)
	p := open(t, h).press("step/b").submit()
	if p.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", p.code)
	}
	if got := p.texts(".jsonform-elsewhere button"); !slices.Equal(got, []string{"a"}) {
		t.Errorf("errors elsewhere = %q", got)
	}
	if !p.has(`.jsonform-outline li[data-invalid] > button[value="step/a"]`) {
		t.Errorf("outline does not mark the invalid step")
	}
	p = p.press("step/a")
	if got := p.texts(`[data-pointer="/a/n"] .jsonform-errors li`); len(got) != 1 {
		t.Errorf("errors on the step = %q", got)
	}
	if p.has(".jsonform-elsewhere") {
		t.Errorf("the step's own errors are listed as elsewhere")
	}
}

func TestWizard_writeOnlyStaysOut(t *testing.T) {
	h := newHandler(t, `{"type":"object","properties":{"password":{"type":"string","writeOnly":true},"profile":{"type":"object","properties":{"bio":{"type":"string"}}}}}`)
	p := open(t, h).press(ActionWizard).set("$/password", "s3cret").press("step/profile")
	if strings.Contains(p.form().OuterHTML(), "s3cret") {
		t.Errorf("a write-only value rides along in hidden inputs")
	}
}

func TestWizard_edges(t *testing.T) {
	t.Run("a value without groups", func(t *testing.T) {
		p := open(t, newHandler(t, `{"type":"string"}`)).press(ActionWizard)
		if !p.has(`input[name="$"]:not([type="hidden"])`) || p.has(".jsonform-next") {
			t.Errorf("scalar wizard = %s", p.form().OuterHTML())
		}
	})

	t.Run("a step that does not exist falls back to the first", func(t *testing.T) {
		p := open(t, newHandler(t, wizardSchema)).post(url.Values{"@mode": {ModeWizard}, "@step": {"/nope"}, "$/name": {"x"}, ActionName: {"refresh"}})
		if got := p.doc.QuerySelector(`input[name="@step"]`).GetAttribute("value"); got != "" {
			t.Errorf("@step = %q, want the root", got)
		}
	})

	t.Run("a root choice", func(t *testing.T) {
		h := newHandler(t, `{"oneOf":[{"type":"object","properties":{"a":{"type":"object","properties":{"x":{"type":"string"}}}}},{"type":"string"}]}`)
		h.Value = jsontext.Value(`{"a":{"x":"1"}}`)
		p := open(t, h).press(ActionWizard)
		if !p.has(`[data-pointer=""] select[name="@variant"]`) || !p.has(`[data-pointer="/a"][data-stub]`) {
			t.Errorf("root choice step = %s", p.form().OuterHTML())
		}
		if got := p.texts(`[data-pointer="/a"] .jsonform-summary`); len(got) != 0 {
			t.Errorf("object summary = %q", got)
		}
	})

	t.Run("live validation only targets rendered lists", func(t *testing.T) {
		h := newHandler(t, wizardSchema)
		h.Value = jsontext.Value(`{"name":"x","address":{"street":"s"}}`)
		p := open(t, h).press(ActionWizard)
		state := controls(p.form())
		req := formRequest(state)
		req.URL.Path = "/form/validate"
		v := fragment(t, serve(t, h, req))
		for el := range v.QuerySelectorSequence("ul") {
			if p.doc.QuerySelector("#"+el.ID()) == nil {
				t.Errorf("validation targets %s, which the page does not have", el.ID())
			}
		}
	})
}

func TestFold(t *testing.T) {
	h := newHandler(t, wizardSchema)
	h.Outline = true
	h.Value = jsontext.Value(`{"name":"Ada","address":{"street":"Main","geo":{"lat":1}},"tags":["x","y"]}`)
	p := open(t, h)
	if b := p.doc.QuerySelector(`.jsonform-outline button[value="fold/address"]`); b == nil || b.TextContent() != "Hide" || b.GetAttribute("aria-label") != "Hide address" {
		t.Fatalf("no hide button for address")
	}
	p = p.press("fold/address")
	stub := p.doc.QuerySelector(`[data-pointer="/address"]`)
	if stub == nil || !stub.HasAttribute("data-stub") || !stub.HasAttribute("data-folded") {
		t.Fatalf("address is not hidden: %v", stub)
	}
	if b := stub.QuerySelector(`button[value="unfold/address"]`); b == nil || b.TextContent() != "Show" {
		t.Errorf("hidden group has no show button")
	}
	var hidden []string
	for el := range stub.QuerySelectorSequence(`input[type="hidden"]`) {
		hidden = append(hidden, el.GetAttribute("name")+"="+el.GetAttribute("value"))
	}
	for _, want := range []string{"$/address/street=Main", "$/address/geo/lat=1", "@folded/address=1"} {
		if !slices.Contains(hidden, want) {
			t.Errorf("hidden group does not carry %s: %q", want, hidden)
		}
	}
	item := p.doc.QuerySelector(`.jsonform-outline li[data-folded]`)
	if item == nil || item.QuerySelector("ol") != nil || item.QuerySelector(`button[value="unfold/address"]`) == nil {
		t.Errorf("outline item of a hidden group = %v", item)
	}
	// Hidden stays hidden across edits, and the value is unchanged.
	p = p.press("add/tags")
	if !p.has(`[data-pointer="/address"][data-folded]`) {
		t.Errorf("the group came back after an edit")
	}
	res := p.submit()
	assertJSONEqual(t, jsontext.Value(res.doc.QuerySelector(".jsonform-result code").TextContent()), `{"name":"Ada","address":{"street":"Main","geo":{"lat":1}},"tags":["x","y",""]}`)

	p = p.press("unfold/address")
	if el := p.doc.QuerySelector(`[data-pointer="/address"]`); el.HasAttribute("data-stub") || !p.has(`input[name="$/address/street"]:not([type="hidden"])`) {
		t.Errorf("show did not bring the group back")
	}
}

func TestReasons(t *testing.T) {
	doc := render(t, `{"type":"object","properties":{
		"a":{"type":"array","items":{"type":"string"},"maxItems":1},
		"one":{"type":"array","items":{"type":"string"},"maxItems":1},
		"t":{"type":"array","prefixItems":[{"type":"string"},{"type":"string"}],"items":false},
		"m":{"type":"object","additionalProperties":{"type":"string"},"maxProperties":2},
		"c":{"type":"object","properties":{"x":{"type":"string"}},"additionalProperties":false},
		"r":{"type":"string","readOnly":true},
		"v":{"oneOf":[{"title":"Dog","type":"string"},{"title":"Cat","type":"integer"},{"title":"Bird","type":"boolean"}]}}}`,
		`{"a":["x"],"t":["p","q"],"m":{"k":"1","l":"2"},"c":{"x":"1","y":"2"},"r":"z","v":"woof"}`)
	for ptr, want := range map[string]string{
		"/a": "At most 1 item.",
		"/t": "Exactly 2 items.",
		"/m": "At most 2 properties.",
		"/c": "No other properties are allowed.",
	} {
		field := doc.QuerySelector(`[data-pointer="` + ptr + `"]`)
		button := field.QuerySelector("button.jsonform-add")
		reason := field.QuerySelector(".jsonform-reason")
		if button == nil || !button.HasAttribute("disabled") || reason == nil || reason.TextContent() != want || button.GetAttribute("aria-describedby") != reason.ID() {
			t.Errorf("%s: add %v, reason %v, want %q", ptr, button, reason, want)
		}
	}
	if b := doc.QuerySelector(`[data-pointer="/one"] button.jsonform-add`); b == nil || b.HasAttribute("disabled") {
		t.Errorf("an array with room has no working add button")
	}
	if got := strings.TrimSpace(doc.QuerySelector(`[data-pointer="/r"] .jsonform-reason`).TextContent()); got != "Read-only" {
		t.Errorf("read-only reason = %q", got)
	}
	note := doc.QuerySelector(`[data-pointer="/v"] .jsonform-inactive`)
	if note == nil || !strings.Contains(note.TextContent(), "Not in use: Cat, Bird.") {
		t.Errorf("inactive variants note = %v", note)
	}
	if doc.QuerySelector(`[data-pointer="/v/x"]`) != nil {
		t.Errorf("fields of an inactive variant are rendered")
	}
}

var _ = io.Discard
