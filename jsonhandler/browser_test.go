package jsonhandler

import (
	"net/url"
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

// jump goes to a wizard step the way an outline or error link does,
// submitting the page's controls with a step action.
func (p *page) jump(key string) *page {
	p.t.Helper()
	state := controls(p.form())
	state.Set(ActionName, ActionWizard+key)
	return p.post(state)
}
